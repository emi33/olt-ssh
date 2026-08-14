// Package database encapsula el acceso a MySQL: lectura de las tablas de
// registro (registro_onu / registro_cliente), carga de esas tablas y consultas
// auxiliares. Devuelve datos crudos; el armado de los comandos de la OLT es
// responsabilidad de cmd/provisionar.
package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"oltssh/internal/config"

	_ "github.com/go-sql-driver/mysql"
)

// ONUInfo contiene los datos basicos de una ONU leidos de la BD.
type ONUInfo struct {
	IdOlt         int
	PuertoOlt     int
	Identificador int
	Sn            string
	Mac           string
	Status        string
}

// RegistroServicio contiene los datos de un cliente activo en CT listos para
// armar el comando service-port en la OLT.
type RegistroServicio struct {
	ID               int64 // registro_cliente.id, para escribir sp_index de vuelta
	Mac              string
	Cliente          sql.NullString
	IpCliente        sql.NullString
	IpCT             sql.NullString
	NroConexion      sql.NullString
	Vlan             int
	Identificador    int
	PuertoOlt        int
	GemId            sql.NullInt64 // eqcliente.gemId puede ser NULL (no se usa: gem-id se fija en 1)
	SPort            sql.NullInt64 // eqcliente.sPort puede ser NULL
	Plan             sql.NullString
	Sn               sql.NullString
	OnuIdentificador sql.NullInt64
	StreamProfile    sql.NullString
	ServiceProfile   sql.NullString
	EstadoPresencia  sql.NullString
	CantidadMacs     sql.NullInt64
	// EstadoRegistro es registro_cliente.estado_registro. Se usa para detectar
	// las filas de plan sin reconocer, que se cargan con el plan en NULL (ver
	// EsPlanNoReconocido).
	EstadoRegistro sql.NullString
}

// DB envuelve la conexión a MySQL.
type DB struct {
	db *sql.DB
}

// New abre la conexión a MySQL a partir de la configuración 'database'.
//
// Verifica la conexión con un Ping y, si falla, reintenta hasta
// cfg.ConnectAttempts veces (esperando cfg.RetryDelay entre intentos). Esto cubre
// los casos en los que la base de datos aún no está lista o hay un corte de red
// transitorio. Si se agotan los intentos, devuelve el mensaje del último error.
func New(cfg config.DBConfig) (*DB, error) {
	// DSN de go-sql-driver/mysql: user:pass@tcp(host:port)/dbname?charset=...
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=%s",
		cfg.Username, cfg.Password, cfg.Host, cfg.Port, cfg.Name, cfg.Charset)

	attempts := cfg.ConnectAttempts
	if attempts < 1 {
		attempts = 1
	}

	var lastErr error
	for intento := 1; intento <= attempts; intento++ {
		db, err := sql.Open("mysql", dsn)
		if err != nil {
			// sql.Open no suele fallar (no abre conexión real); aun así lo tratamos
			// como intento fallido para no perder el detalle del error.
			lastErr = err
		} else if err := db.Ping(); err != nil {
			db.Close()
			lastErr = err
		} else {
			if intento > 1 {
				fmt.Printf("Conexión a la base de datos establecida en el intento %d/%d.\n", intento, attempts)
			}
			return &DB{db: db}, nil
		}

		fmt.Printf("Intento %d/%d de conexión a la base de datos fallido: %v\n", intento, attempts, lastErr)
		if intento < attempts {
			fmt.Printf("Reintentando en %s...\n", cfg.RetryDelay)
			time.Sleep(cfg.RetryDelay)
		}
	}

	return nil, fmt.Errorf("error de conexión a la base de datos tras %d intento(s): %w", attempts, lastErr)
}

// Close cierra el pool de conexiones.
func (d *DB) Close() error {
	return d.db.Close()
}

// GetONUsDelPuerto devuelve las ONUs registradas en la BD para una OLT y puerto.
func (d *DB) GetONUsDelPuerto(ctx context.Context, idOlt, puertoOlt int) ([]ONUInfo, error) {
	query := `SELECT idOlt, puertoOlt, identificador, sn, mac, status
		FROM onu WHERE idOlt = ? AND puertoOlt = ?
		ORDER BY identificador`

	rows, err := d.db.QueryContext(ctx, query, idOlt, puertoOlt)
	if err != nil {
		return nil, fmt.Errorf("consultando ONUs: %w", err)
	}
	defer rows.Close()

	var onus []ONUInfo
	for rows.Next() {
		var o ONUInfo
		if err := rows.Scan(&o.IdOlt, &o.PuertoOlt, &o.Identificador, &o.Sn, &o.Mac, &o.Status); err != nil {
			return nil, fmt.Errorf("leyendo ONU: %w", err)
		}
		onus = append(onus, o)
	}
	return onus, rows.Err()
}

// GetOLTIPAdmin devuelve la IP de administración (ipAdmin) registrada en la
// tabla `olt` para el idOlt dado. Sirve de guardrail: permite verificar que el
// OLT_HOST al que se conecta el SSH sea realmente la OLT de ese idOlt y no otra.
// found=false indica que el idOlt no está en la tabla (sin error).
func (d *DB) GetOLTIPAdmin(ctx context.Context, idOlt int) (ip string, found bool, err error) {
	err = d.db.QueryRowContext(ctx, "SELECT ipAdmin FROM olt WHERE idOlt = ?", idOlt).Scan(&ip)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("consultando ipAdmin de idOlt %d: %w", idOlt, err)
	}
	return ip, true, nil
}

// FilaCarga es una fila de la consulta principal del flujo de carga a las tablas
// de registro. Una fila = una MAC de eqcliente cruzada con su cliente activo en CT.
type FilaCarga struct {
	MacWan           string
	Pppoe            string
	IpCliente        sql.NullString
	IpControlador    sql.NullString
	FechaCT          sql.NullString
	NroCliente       sql.NullInt64
	Vlan             int
	PuertoPon        int
	OnuID            int
	Indice           sql.NullInt64 // eq.idx: índice de la MAC en la OLT Kingtype
	EsActivo         int
	FechaEq          sql.NullString
	Plan             sql.NullString
	Sn               sql.NullString
	OnuIdentificador sql.NullInt64
	EstadoPresencia  sql.NullString
	CantidadMacs     sql.NullInt64
}

// GetFilasCargaPrincipal ejecuta la consulta principal del flujo de carga. La
// lista de controladores (ipCT) arma dinámicamente la cláusula IN.
func (d *DB) GetFilasCargaPrincipal(ctx context.Context, idOlt int, fechaDesde string, controladores []string) ([]FilaCarga, error) {
	if len(controladores) == 0 {
		return nil, fmt.Errorf("no hay controladores (PROVISIONING_CONTROLADORES vacío)")
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(controladores)), ",")
	query := fmt.Sprintf(queryCargaPrincipal, ph)

	args := make([]any, 0, 2+len(controladores))
	args = append(args, idOlt, fechaDesde)
	for _, c := range controladores {
		args = append(args, c)
	}

	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("ejecutando consulta principal de carga: %w", err)
	}
	defer rows.Close()

	var filas []FilaCarga
	for rows.Next() {
		var f FilaCarga
		if err := rows.Scan(
			&f.MacWan, &f.Pppoe, &f.IpCliente, &f.IpControlador, &f.FechaCT,
			&f.NroCliente, &f.Vlan, &f.PuertoPon, &f.OnuID, &f.Indice,
			&f.EsActivo, &f.FechaEq, &f.Plan, &f.Sn, &f.OnuIdentificador,
			&f.EstadoPresencia, &f.CantidadMacs,
		); err != nil {
			return nil, fmt.Errorf("leyendo fila de carga: %w", err)
		}
		filas = append(filas, f)
	}
	return filas, rows.Err()
}

// GetPlanesPorNombre devuelve un mapa nombre_plan -> id de la tabla planes.
func (d *DB) GetPlanesPorNombre(ctx context.Context) (map[string]int, error) {
	rows, err := d.db.QueryContext(ctx, "SELECT id, nombre FROM olt_test.planes")
	if err != nil {
		return nil, fmt.Errorf("cargando planes: %w", err)
	}
	defer rows.Close()
	m := make(map[string]int)
	for rows.Next() {
		var id int
		var nombre string
		if err := rows.Scan(&id, &nombre); err != nil {
			return nil, err
		}
		m[strings.TrimSpace(nombre)] = id
	}
	return m, rows.Err()
}

// GetGemPortPorVlan devuelve un mapa vlan -> gem_port_id (= gem-id) desde
// gem_config para el idOlt dado.
func (d *DB) GetGemPortPorVlan(ctx context.Context, idOlt int) (map[int]int, error) {
	rows, err := d.db.QueryContext(ctx, "SELECT vlan, gem_port_id FROM olt_test.gem_config WHERE id_olt = ?", idOlt)
	if err != nil {
		return nil, fmt.Errorf("cargando gem_config: %w", err)
	}
	defer rows.Close()
	m := make(map[int]int)
	for rows.Next() {
		var vlan, gemPort int
		if err := rows.Scan(&vlan, &gemPort); err != nil {
			return nil, err
		}
		m[vlan] = gemPort
	}
	return m, rows.Err()
}

// insertRegistroOnu / insertRegistroCliente: upsert de una fila de cada tabla de
// registro. Las tablas se vacían antes de escribir (ver ReemplazarRegistros), así
// que el ON DUPLICATE KEY no cubre corridas anteriores sino las colisiones DENTRO
// de la misma corrida: dos ONUs con el mismo SN real (uq_regonu_olt_sn) o dos
// posiciones que reclaman la misma MAC (uq_olt_mac). Sin él, esas filas abortarían
// la transacción entera; con él, gana la última y el resto de la carga sobrevive.
const insertRegistroOnu = `
INSERT INTO olt_test.registro_onu
    (id_olt, puerto, ont_id, sn, estado_presencia, cantidad_macs, desc_ont,
     estado_registro, listo_para_cargar, motivo)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
    sn=VALUES(sn), estado_presencia=VALUES(estado_presencia),
    cantidad_macs=VALUES(cantidad_macs), desc_ont=VALUES(desc_ont),
    estado_registro=VALUES(estado_registro), listo_para_cargar=VALUES(listo_para_cargar),
    motivo=VALUES(motivo), fecha_actualizacion=CURRENT_TIMESTAMP`
const insertRegistroCliente = `
INSERT INTO olt_test.registro_cliente
    (id_olt, puerto, ont_id, mac_wan, pppoe, vlan, plan_id, plan, gem_id, idx, fecha_eq,
     nro_cliente, ip_cliente, ip_controlador, estado_registro, listo_para_cargar, motivo)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
    puerto=VALUES(puerto), ont_id=VALUES(ont_id), pppoe=VALUES(pppoe), vlan=VALUES(vlan),
    plan_id=VALUES(plan_id), plan=VALUES(plan), gem_id=VALUES(gem_id), idx=VALUES(idx),
    fecha_eq=VALUES(fecha_eq), nro_cliente=VALUES(nro_cliente), ip_cliente=VALUES(ip_cliente),
    ip_controlador=VALUES(ip_controlador), estado_registro=VALUES(estado_registro),
    listo_para_cargar=VALUES(listo_para_cargar), motivo=VALUES(motivo),
    fecha_actualizacion=CURRENT_TIMESTAMP`

// rellenoOnusFaltantes agrega a registro_onu las ONUs de la tabla `onu` (idOlt)
// que todavía NO estén (por posición), para que registro_onu termine con la misma
// cantidad de filas que `onu`. INSERT IGNORE respeta las posiciones ya escritas y
// sólo inserta las que faltan, con estado 'repaso_relleno'. El SN 'UNKNOWN'/vacío
// se guarda como NULL para no chocar con uq_regonu_olt_sn.
const rellenoOnusFaltantes = `
INSERT IGNORE INTO olt_test.registro_onu
    (id_olt, puerto, ont_id, sn, estado_presencia, cantidad_macs, estado_registro, listo_para_cargar)
SELECT o.idOlt, o.puertoOlt, o.identificador,
       NULLIF(NULLIF(o.sn, ''), 'UNKNOWN'),
       o.estadoPresencia, o.cantidadMacs, 'repaso_relleno', 0
FROM olt_test.onu o
WHERE o.idOlt = ?
  AND o.estadoPresencia <> 'ausente_sin_sn'`

// ResultadoEscritura resume lo que hizo ReemplazarRegistros.
type ResultadoEscritura struct {
	BorradasOnu     int64
	BorradasCliente int64
	Rellenadas      int64
}

// ReemplazarRegistros deja las tablas de registro de una OLT exactamente con lo
// que produjo esta corrida: borra TODO lo anterior de ese id_olt y escribe lo
// nuevo, dentro de UNA sola transacción.
//
// El borrado previo es lo que evita los falsos matcheos: sin él, una MAC que dejó
// de existir, un cliente que cambió de posición o una ONU que se dio de baja
// quedarían para siempre en la tabla, porque el upsert sólo pisa lo que la
// corrida vuelve a emitir. Y va en transacción porque borrar-y-escribir en dos
// pasos sueltos deja una ventana en la que un corte del programa o del servidor
// dejaría las tablas vacías y sin cargar; con la transacción, o queda todo lo
// nuevo o sigue intacto todo lo viejo.
//
// El orden respeta la FK fk_regcliente_regonu (no tiene ON DELETE CASCADE): se
// borran los clientes antes que las ONUs y se insertan las ONUs antes que los
// clientes. Con rellenar=true agrega al final las ONUs faltantes (lo usa el
// repaso, que apunta a dejar registro_onu pareja con `onu`).
func (d *DB) ReemplazarRegistros(ctx context.Context, idOlt int, onus []RegistroOnu, clientes []RegistroCliente, rellenar bool) (ResultadoEscritura, error) {
	var res ResultadoEscritura

	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return res, fmt.Errorf("abriendo transacción: %w", err)
	}
	// Rollback en cualquier salida por error; tras un Commit exitoso es un no-op.
	defer tx.Rollback()

	borrCli, err := tx.ExecContext(ctx, "DELETE FROM olt_test.registro_cliente WHERE id_olt = ?", idOlt)
	if err != nil {
		return res, fmt.Errorf("borrando registro_cliente de idOlt=%d: %w", idOlt, err)
	}
	res.BorradasCliente, _ = borrCli.RowsAffected()

	borrOnu, err := tx.ExecContext(ctx, "DELETE FROM olt_test.registro_onu WHERE id_olt = ?", idOlt)
	if err != nil {
		return res, fmt.Errorf("borrando registro_onu de idOlt=%d: %w", idOlt, err)
	}
	res.BorradasOnu, _ = borrOnu.RowsAffected()

	stmtOnu, err := tx.PrepareContext(ctx, insertRegistroOnu)
	if err != nil {
		return res, fmt.Errorf("preparando insert de registro_onu: %w", err)
	}
	defer stmtOnu.Close()
	for _, r := range onus {
		if _, err := stmtOnu.ExecContext(ctx,
			r.IDOlt, r.Puerto, r.OntID, r.Sn, r.EstadoPresencia, r.CantidadMacs,
			r.DescOnt, r.Estado, boolToInt(r.Listo), nullIfEmpty(r.Motivo)); err != nil {
			return res, fmt.Errorf("guardando registro_onu %d/%d: %w", r.Puerto, r.OntID, err)
		}
	}

	stmtCli, err := tx.PrepareContext(ctx, insertRegistroCliente)
	if err != nil {
		return res, fmt.Errorf("preparando insert de registro_cliente: %w", err)
	}
	defer stmtCli.Close()
	for _, r := range clientes {
		if _, err := stmtCli.ExecContext(ctx,
			r.IDOlt, r.Puerto, r.OntID, r.MacWan, nullIfEmpty(r.Pppoe), r.Vlan, r.PlanID, r.Plan, r.GemID,
			r.Idx, r.FechaEq, r.NroCliente, r.IpCliente, r.IpControlador, r.Estado, boolToInt(r.Listo),
			nullIfEmpty(r.Motivo)); err != nil {
			return res, fmt.Errorf("guardando registro_cliente %s: %w", r.MacWan, err)
		}
	}

	if rellenar {
		rell, err := tx.ExecContext(ctx, rellenoOnusFaltantes, idOlt)
		if err != nil {
			return res, fmt.Errorf("rellenando onus faltantes: %w", err)
		}
		res.Rellenadas, _ = rell.RowsAffected()
	}

	if err := tx.Commit(); err != nil {
		return res, fmt.Errorf("confirmando la transacción: %w", err)
	}
	return res, nil
}

// ContarRegistroOnu y ContarOnu devuelven la cantidad de filas de cada tabla
// para una OLT, para verificar que quedaron parejas tras el relleno.
func (d *DB) ContarRegistroOnu(ctx context.Context, idOlt int) (int, error) {
	var n int
	err := d.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM olt_test.registro_onu WHERE id_olt = ?", idOlt).Scan(&n)
	return n, err
}

// ContarOnu cuenta las ONUs de la OLT EXCLUYENDO las ausente_sin_sn (que no se
// cargan), para poder comparar de igual a igual contra registro_onu.
func (d *DB) ContarOnu(ctx context.Context, idOlt int) (int, error) {
	var n int
	err := d.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM olt_test.onu WHERE idOlt = ? AND estadoPresencia <> 'ausente_sin_sn'", idOlt).Scan(&n)
	return n, err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// nullIfEmpty devuelve NULL para un string vacío (para columnas nullable).
func nullIfEmpty(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

// OnuBasica es una fila de la tabla onu (universo de ONUs de la OLT).
type OnuBasica struct {
	Puerto          int
	OntID           int
	Sn              sql.NullString
	Mac             sql.NullString
	EstadoPresencia sql.NullString
	CantidadMacs    sql.NullInt64
}

// GetOnusIdOlt devuelve todas las ONUs registradas de una OLT (tabla onu).
func (d *DB) GetOnusIdOlt(ctx context.Context, idOlt int) ([]OnuBasica, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT puertoOlt, identificador, sn, mac, estadoPresencia, cantidadMacs
		   FROM olt_test.onu WHERE idOlt = ?`, idOlt)
	if err != nil {
		return nil, fmt.Errorf("cargando onus idOlt=%d: %w", idOlt, err)
	}
	defer rows.Close()
	var out []OnuBasica
	for rows.Next() {
		var o OnuBasica
		if err := rows.Scan(&o.Puerto, &o.OntID, &o.Sn, &o.Mac, &o.EstadoPresencia, &o.CantidadMacs); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// OnuVerificada es una posición de la tabla `onu`, de CUALQUIER OLT, cuya MAC se
// verificó contra su SN. Lleva el SN tal cual está escrito para poder nombrarlo
// en un motivo (cada OLT usa su propia convención: con guión o sin él).
type OnuVerificada struct {
	IDOlt  int
	Puerto int
	OntID  int
	Sn     string
}

// GetOnusVerificadasTodasLasOlts trae las ONUs verificadas de TODAS las OLTs, no
// sólo la que se está repasando. Sirve para detectar equipos que se mudaron: si
// un SN de esta OLT aparece verificado en otra, el equipo ya no está acá y no hay
// que migrarlo.
//
// El filtro por estadoPresencia va en el SQL y no en Go porque reduce el
// resultado a las pocas miles de ONUs realmente activas del parque, en vez de
// traer la tabla entera. Se descartan también los SN vacíos y 'UNKNOWN': no
// identifican un equipo y harían que todos los desconocidos se "dupliquen" entre
// sí.
func (d *DB) GetOnusVerificadasTodasLasOlts(ctx context.Context) ([]OnuVerificada, error) {
	const q = `
SELECT idOlt, puertoOlt, identificador, sn
  FROM olt_test.onu
 WHERE estadoPresencia = 'mac_verificada'
   AND sn IS NOT NULL AND TRIM(sn) <> '' AND UPPER(TRIM(sn)) <> 'UNKNOWN'`
	rows, err := d.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("consultando onus verificadas de todas las olts: %w", err)
	}
	defer rows.Close()
	var out []OnuVerificada
	for rows.Next() {
		var o OnuVerificada
		if err := rows.Scan(&o.IDOlt, &o.Puerto, &o.OntID, &o.Sn); err != nil {
			return nil, err
		}
		o.Sn = strings.TrimSpace(o.Sn)
		out = append(out, o)
	}
	return out, rows.Err()
}

// MacRepaso es una MAC real del histórico (rn=1) con su ONU (por posición) y su
// cliente en CT (si lo tiene). Excluye placeholders 00:00.
type MacRepaso struct {
	Mac           string
	Puerto        int
	OntID         int
	Vlan          int
	Idx           sql.NullInt64
	FechaEq       sql.NullString // eq.fechaHora (última aparición de la MAC)
	Pppoe         sql.NullString
	Activo        sql.NullInt64
	FechaCt       sql.NullString // ct.fecha (para elegir el cliente más reciente)
	Plan          sql.NullString
	NroCliente    sql.NullInt64
	IpCliente     sql.NullString
	IpControlador sql.NullString
}

// GetMacsHistoricasRepaso trae la última aparición de cada SERVICIO (mac, vlan)
// de la OLT con su cliente en CT (fecha>=corte, en CUALQUIER controlador). Excluye
// placeholders 00:00. La distinción de "CT configurado vs otro CT" se resuelve en
// Go con la lista de controladores (una MAC puede aparecer en varios CT, se trae
// cada match).
//
// La ventana parte por (mac, vlan) y NO sólo por mac: el PK de eqcliente es
// (idOlt, puertoOlt, identificador, mac, vlan, fechaHora), o sea que la vlan es
// parte de la identidad de la fila y una misma MAC con dos vlans son dos
// servicios distintos, cada uno con su propio idx. Partiendo sólo por mac se
// perdían todos los servicios menos el visto más recientemente, en silencio.
//
// Tampoco se parte por posición: así cada servicio resuelve a la posición de su
// última aparición y una MAC que se mudó de puerto no deja fantasmas en el puerto
// viejo (se conserva la regla de que una MAC vive donde se la vio por última vez).
//
// Límite conocido: observacionesct mapea mac -> cliente SIN vlan, y el join es por
// MAC sola. Un cliente cuya MAC presta dos servicios queda atribuido a las dos
// filas (mismo pppoe, distinta vlan). Es inherente a los datos de origen; por eso
// registro_cliente ya no lleva la unique key por pppoe.
func (d *DB) GetMacsHistoricasRepaso(ctx context.Context, idOlt int, fechaDesde string) ([]MacRepaso, error) {
	const query = `
SELECT eq.mac, eq.puertoOlt, eq.identificador, eq.vlan, eq.idx, eq.fechaHora,
       ct.cliente, ct.activo, ct.fecha, c.plan, c.nroConexion, ct.ipCliente, ct.ipCT
FROM (
    SELECT *, ROW_NUMBER() OVER (PARTITION BY mac, vlan ORDER BY fechaHora DESC) AS rn
    FROM olt_test.eqcliente WHERE idOlt = ?
) eq
LEFT JOIN observacionesct ct ON UPPER(ct.mac) = UPPER(eq.mac)
      AND ct.fecha >= ?
LEFT JOIN conexiones c ON ct.cliente = c.pppoe
WHERE eq.rn = 1 AND REPLACE(eq.mac, ':', '') <> '000000000000'`
	rows, err := d.db.QueryContext(ctx, query, idOlt, fechaDesde)
	if err != nil {
		return nil, fmt.Errorf("consulta repaso de macs: %w", err)
	}
	defer rows.Close()
	var out []MacRepaso
	for rows.Next() {
		var m MacRepaso
		if err := rows.Scan(&m.Mac, &m.Puerto, &m.OntID, &m.Vlan, &m.Idx, &m.FechaEq,
			&m.Pppoe, &m.Activo, &m.FechaCt, &m.Plan, &m.NroCliente, &m.IpCliente, &m.IpControlador); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetPlaceholderIdx devuelve, por posición (puerto,ont_id), los índices con MAC
// placeholder 00:00 (fecha>=corte). Sirve para el caso 4 (puertos/índices vacíos).
//
// Descarta los idx NULL y los idx = 0: un placeholder sin índice real no señala
// ninguna boca concreta de la ONU y sólo infla el listado. El otro filtro —el
// placeholder cuyo índice ya ocupa una MAC real— necesita las MACs ya resueltas
// y se aplica después, con PlaceholdersLibres.
func (d *DB) GetPlaceholderIdx(ctx context.Context, idOlt int, fechaDesde string) (map[[2]int][]int, error) {
	// DISTINCT: el PK de eqcliente incluye vlan y fechaHora, así que el mismo idx
	// con MAC 00:00 vuelve una vez por vlan y por lectura. Sin él, el motivo salía
	// como "indices_00: 3,3,3,5,5".
	rows, err := d.db.QueryContext(ctx,
		`SELECT DISTINCT puertoOlt, identificador, idx FROM olt_test.eqcliente
		  WHERE idOlt = ? AND REPLACE(mac, ':', '') = '000000000000' AND fechaHora >= ?
		    AND idx IS NOT NULL AND idx <> 0`,
		idOlt, fechaDesde)
	if err != nil {
		return nil, fmt.Errorf("consulta placeholders 00:00: %w", err)
	}
	defer rows.Close()
	m := map[[2]int][]int{}
	for rows.Next() {
		var p, o int
		var idx sql.NullInt64
		if err := rows.Scan(&p, &o, &idx); err != nil {
			return nil, err
		}
		if idx.Valid {
			m[[2]int{p, o}] = append(m[[2]int{p, o}], int(idx.Int64))
		}
	}
	return m, rows.Err()
}

// EstadoPrevio es el estado con el que la PRIMERA pasada dejó una posición.
type EstadoPrevio struct {
	Estado string
	Listo  bool
}

// GetPosicionesPrincipal devuelve, por posición (puerto,ont_id), el estado con
// que la PRIMERA pasada la cargó (estado principal_*). Sirve para dos cosas: para
// saber si el repaso sobrescribe (posición ocupada) o agrega, y para poder
// RE-EMITIR tal cual las posiciones que el repaso decide no tocar.
//
// Devuelve el estado real y no un simple booleano porque el repaso vacía y
// reescribe las tablas: si asumiera que toda posición ya cargada era
// 'principal_listo', promovería a listo las que la 1ª pasada había rechazado
// (p. ej. 'principal_plan_no_reconocido', que también matchea 'principal_%').
//
// Lee de registro_onu y no de registro_cliente porque tiene UNA sola fila por
// posición (uq_olt_puerto_ont), así que no hay que desempatar entre filas cliente
// con estados distintos. Para las posiciones de una sola MAC —las únicas que el
// repaso puede re-emitir— el estado de la ONU y el del cliente son el mismo (ver
// ClasificarPrimeraPasada: sin multi-MAC ni cámara, el cliente hereda el mismo
// clasificarFila que la ONU).
func (d *DB) GetPosicionesPrincipal(ctx context.Context, idOlt int) (map[[2]int]EstadoPrevio, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT puerto, ont_id, estado_registro, listo_para_cargar
		   FROM olt_test.registro_onu
		  WHERE id_olt = ? AND estado_registro LIKE 'principal_%'`, idOlt)
	if err != nil {
		return nil, fmt.Errorf("consulta posiciones principal: %w", err)
	}
	defer rows.Close()
	m := map[[2]int]EstadoPrevio{}
	for rows.Next() {
		var p, o int
		var estado string
		var listo int
		if err := rows.Scan(&p, &o, &estado, &listo); err != nil {
			return nil, err
		}
		m[[2]int{p, o}] = EstadoPrevio{Estado: estado, Listo: listo == 1}
	}
	return m, rows.Err()
}

// AsignacionSP asocia el índice de service-port que quedó creado en la OLT con
// las filas de registro_cliente que ese service-port cubre. Son varias filas
// cuando una misma ONU+VLAN tiene más de un cliente (la unicidad de
// registro_cliente es por id_olt+mac_wan+vlan, no por ONU+VLAN): todas comparten
// el mismo service-port y, por lo tanto, el mismo índice.
type AsignacionSP struct {
	SPIndex int
	IDs     []int64
}

// GuardarSPIndex escribe el índice de service-port en registro_cliente.sp_index
// para las filas indicadas. Se llama después de ejecutar en la OLT y solo con
// los service-port que se crearon sin error, de modo que sp_index refleje lo que
// realmente quedó configurado en el equipo.
//
// Va todo en una transacción: o se registra la corrida entera o no se registra
// nada, para no dejar la tabla a medio actualizar si algo falla en el medio.
// Devuelve la cantidad de filas efectivamente modificadas.
func (d *DB) GuardarSPIndex(ctx context.Context, asignaciones []AsignacionSP) (int64, error) {
	if len(asignaciones) == 0 {
		return 0, nil
	}

	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("abriendo transacción para sp_index: %w", err)
	}
	// Si se sale por error, Rollback deshace lo aplicado. Tras un Commit
	// exitoso este Rollback es un no-op (devuelve ErrTxDone, se ignora).
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, "UPDATE registro_cliente SET sp_index = ? WHERE id = ?")
	if err != nil {
		return 0, fmt.Errorf("preparando update de sp_index: %w", err)
	}
	defer stmt.Close()

	var total int64
	for _, a := range asignaciones {
		for _, id := range a.IDs {
			res, err := stmt.ExecContext(ctx, a.SPIndex, id)
			if err != nil {
				return 0, fmt.Errorf("guardando sp_index=%d en registro_cliente id=%d: %w", a.SPIndex, id, err)
			}
			n, err := res.RowsAffected()
			if err != nil {
				return 0, fmt.Errorf("leyendo filas afectadas de sp_index: %w", err)
			}
			total += n
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("confirmando transacción de sp_index: %w", err)
	}
	return total, nil
}

// GetRegistrosProvisioning devuelve los clientes activos en los CT configurados,
// cruzados con eqcliente/conexiones/onu para la OLT idOlt indicada. El llamador
// filtra por estado/plan antes de enviar comandos a la OLT.
func (d *DB) GetRegistrosProvisioning(ctx context.Context, idOlt int) ([]RegistroServicio, error) {
	rows, err := d.db.QueryContext(ctx, queryRegistrosProvisioning, idOlt)
	if err != nil {
		return nil, fmt.Errorf("ejecutando consulta de provisioning: %w", err)
	}
	defer rows.Close()

	var registros []RegistroServicio
	for rows.Next() {
		var r RegistroServicio
		if err := rows.Scan(
			&r.ID, &r.Mac, &r.Cliente, &r.IpCliente, &r.IpCT, &r.NroConexion,
			&r.Vlan, &r.Identificador, &r.PuertoOlt, &r.GemId, &r.SPort,
			&r.Plan, &r.Sn, &r.OnuIdentificador, &r.StreamProfile, &r.ServiceProfile,
			&r.EstadoPresencia, &r.CantidadMacs, &r.EstadoRegistro,
		); err != nil {
			return nil, fmt.Errorf("leyendo fila de provisioning: %w", err)
		}
		registros = append(registros, r)
	}
	return registros, rows.Err()
}
