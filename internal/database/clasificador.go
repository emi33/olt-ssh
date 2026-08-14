package database

import (
	"database/sql"
	"strings"
)

// RegistroOnu es una fila lista para guardar en registro_onu.
type RegistroOnu struct {
	IDOlt           int
	Puerto          int
	OntID           int
	Sn              sql.NullString
	EstadoPresencia sql.NullString
	CantidadMacs    int
	DescOnt         string
	Estado          string
	Listo           bool
	Motivo          string // detalle libre (p. ej. índices 00:00 del caso 4)
}

// RegistroCliente es una fila lista para guardar en registro_cliente.
type RegistroCliente struct {
	IDOlt         int
	Puerto        int
	OntID         int
	MacWan        string
	Pppoe         string
	Vlan          int
	PlanID        sql.NullInt64
	Plan          string // texto crudo del plan (vacío si no trae)
	GemID         sql.NullInt64
	Idx           sql.NullInt64
	FechaEq       sql.NullString // última fechaHora de la MAC en eqcliente
	NroCliente    sql.NullInt64
	IpCliente     sql.NullString
	IpControlador sql.NullString
	Estado        string
	Listo         bool
	Motivo        string // detalle libre por MAC (p. ej. el conteo de clientes en CT)
}

// Estados de la primera pasada (prefijo "principal_"). Texto libre en la tabla.
const (
	EstadoListo               = "principal_listo"                  // 1 onu : 1 mac : 1 cliente activo (cargable)
	EstadoVlan1001Aire        = "principal_vlan_1001_aire"         // caso 9
	EstadoVlanMayor3000       = "principal_vlan_mayor_3000_con_cliente" // caso 6
	EstadoMacSinOnu           = "principal_mac_sin_onu_en_puerto"  // caso 10
	EstadoNoCargable          = "principal_estado_no_cargable"     // estadoPresencia no cargable
	EstadoPlanNoReconocido    = "principal_plan_no_reconocido"
	EstadoOnuDosMacs          = "principal_onu_dos_macs_dos_clientes"   // caso 1
	EstadoOnuMultimacClientes = "principal_onu_multimac_con_clientes"   // caso 3 (todas con cliente)
)

// estadosCargables replica el filtro de estadoPresencia del provisioner.
var estadosCargables = map[string]bool{
	"mac_verificada":    true,
	"mac_sin_verificar": true,
	"macs_multiples":    true,
}

// ClasificarPrimeraPasada agrupa las filas de la consulta principal por ONU
// (puerto+onu_id), las clasifica en un estado y arma las filas listas para
// guardar en registro_onu / registro_cliente. Es una función pura (sin BD),
// testeable. `planPorNombre` y `gemPorVlan` resuelven plan_id y gem_id.
//
// Regla de cascada: si una ONU tiene 2+ MACs (excepción), TANTO la ONU como
// TODOS sus clientes comparten el estado de la excepción (no van "listo").
func ClasificarPrimeraPasada(filas []FilaCarga, idOlt int, planPorNombre map[string]int, gemPorVlan map[int]int) ([]RegistroOnu, []RegistroCliente) {
	type pos struct{ puerto, ont int }

	// Agrupar por ONU conservando orden de aparición y deduplicando por MAC.
	var orden []pos
	grupos := map[pos][]FilaCarga{}
	vistaMac := map[pos]map[string]bool{}
	for _, f := range filas {
		p := pos{f.PuertoPon, f.OnuID}
		if _, ok := grupos[p]; !ok {
			orden = append(orden, p)
			vistaMac[p] = map[string]bool{}
		}
		mac := strings.ToUpper(strings.TrimSpace(f.MacWan))
		if vistaMac[p][mac] {
			continue // misma MAC repetida (p. ej. matchea 2 CT): una sola fila cliente
		}
		vistaMac[p][mac] = true
		grupos[p] = append(grupos[p], f)
	}

	var onus []RegistroOnu
	var clientes []RegistroCliente

	for _, p := range orden {
		g := grupos[p]
		// ausente_sin_sn: la ONU no existe registrada (solo historial) -> no se carga.
		if esAusenteSinSn(g[0].EstadoPresencia) {
			continue
		}
		multi := len(g) >= 2
		camara := grupoConVlanCamara(g) // alguna MAC con vlan > 3000
		heredanEstadoOnu := multi || camara

		// Estado a nivel ONU. La cámara (vlan>3000) tiene PRIORIDAD sobre multi-MAC.
		var estadoOnu string
		var listoOnu bool
		switch {
		case camara:
			estadoOnu, listoOnu = EstadoVlanMayor3000, false
		case multi:
			if len(g) == 2 {
				estadoOnu = EstadoOnuDosMacs
			} else {
				estadoOnu = EstadoOnuMultimacClientes
			}
			listoOnu = false
		default:
			estadoOnu, listoOnu = clasificarFila(g[0], planPorNombre)
		}

		// registro_onu (uno por grupo). Datos de onu de la primera fila.
		f0 := g[0]
		onus = append(onus, RegistroOnu{
			IDOlt:           idOlt,
			Puerto:          p.puerto,
			OntID:           p.ont,
			Sn:              snParaRegistro(f0.Sn),
			EstadoPresencia: f0.EstadoPresencia,
			CantidadMacs:    int(f0.CantidadMacs.Int64),
			DescOnt:         descripcion(f0),
			Estado:          estadoOnu,
			Listo:           listoOnu,
		})

		// registro_cliente (uno por MAC). En multi-MAC heredan el estado ONU.
		for _, f := range g {
			estadoCli, listoCli := estadoOnu, listoOnu
			if !heredanEstadoOnu {
				estadoCli, listoCli = clasificarFila(f, planPorNombre)
			}
			clientes = append(clientes, RegistroCliente{
				IDOlt:         idOlt,
				Puerto:        p.puerto,
				OntID:         p.ont,
				MacWan:        f.MacWan,
				Pppoe:         f.Pppoe,
				Vlan:          f.Vlan,
				PlanID:        buscarPlan(f.Plan, planPorNombre),
				Plan:          nn(f.Plan),
				GemID:         buscarGem(f.Vlan, gemPorVlan),
				Idx:           f.Indice,
				FechaEq:       f.FechaEq,
				NroCliente:    f.NroCliente,
				IpCliente:     f.IpCliente,
				IpControlador: f.IpControlador,
				Estado:        estadoCli,
				Listo:         listoCli,
			})
		}
	}
	return onus, clientes
}

// esAusenteSinSn indica si la ONU está en estado 'ausente_sin_sn': esas ONUs NO
// existen registradas de verdad, solo quedan en la BD como historial. No se
// cargan en las tablas de registro (ni en cargar ni en repaso).
func esAusenteSinSn(sp sql.NullString) bool {
	return sp.Valid && strings.EqualFold(strings.TrimSpace(sp.String), "ausente_sin_sn")
}

// snParaRegistro normaliza el SN para guardarlo: vacío o 'UNKNOWN' -> NULL. Es
// clave porque registro_onu tiene UNIQUE(id_olt, sn): varios 'UNKNOWN' chocarían
// (se pisan/pierden filas); con NULL, MySQL permite varios y no se pierde ninguna.
func snParaRegistro(sn sql.NullString) sql.NullString {
	v := strings.TrimSpace(sn.String)
	if !sn.Valid || v == "" || strings.EqualFold(v, "UNKNOWN") {
		return sql.NullString{}
	}
	return sql.NullString{String: v, Valid: true}
}

// vlanEsCamara: las vlan > 3000 (4000, 3996, 3991, 3990, ...) son cámaras.
func vlanEsCamara(vlan int) bool { return vlan > 3000 }

// grupoConVlanCamara indica si alguna fila del grupo (ONU) tiene vlan de cámara.
func grupoConVlanCamara(g []FilaCarga) bool {
	for _, f := range g {
		if vlanEsCamara(f.Vlan) {
			return true
		}
	}
	return false
}

// clasificarFila decide el estado de una fila individual (ONU de 1 sola MAC).
func clasificarFila(f FilaCarga, planPorNombre map[string]int) (estado string, listo bool) {
	switch {
	case f.Vlan == 1001:
		return EstadoVlan1001Aire, false
	case f.Vlan > 3000:
		return EstadoVlanMayor3000, false
	case !tieneOnu(f):
		return EstadoMacSinOnu, false
	case !f.EstadoPresencia.Valid || !estadosCargables[strings.ToLower(strings.TrimSpace(f.EstadoPresencia.String))]:
		return EstadoNoCargable, false
	case buscarPlan(f.Plan, planPorNombre).Valid == false:
		// Va LISTO igual: el cliente existe y su servicio hay que darlo de alta;
		// lo único que falta es el plan, que todavía no está cargado en la OLT.
		// Esos planes se cargan a mano en el equipo, fuera de este programa, así
		// que el service-port se crea con el plan vacío y se completa después.
		return EstadoPlanNoReconocido, true
	default:
		return EstadoListo, true
	}
}

// EsPlanNoReconocido indica si un estado_registro es el del plan sin reconocer,
// sea cual sea la pasada que lo escribió ('principal_', 'repaso_',
// 'sobrescrito_repaso_'). Estas posiciones SÍ se cargan: el cliente existe y su
// servicio hay que darlo de alta; lo único que falta es el plan, que se carga a
// mano en el equipo. La usan el repaso (para forzar listo_para_cargar) y
// cmd/provisionar (para armar el service-port sin traffic-in/out).
func EsPlanNoReconocido(estado string) bool {
	return strings.HasSuffix(strings.TrimSpace(estado), "plan_no_reconocido")
}

// tieneOnu indica si la fila tiene una ONU registrada (SN real y onu_identificador).
func tieneOnu(f FilaCarga) bool {
	if !f.OnuIdentificador.Valid {
		return false
	}
	sn := strings.TrimSpace(f.Sn.String)
	return f.Sn.Valid && sn != "" && !strings.EqualFold(sn, "UNKNOWN")
}

func buscarPlan(plan sql.NullString, planPorNombre map[string]int) sql.NullInt64 {
	if !plan.Valid {
		return sql.NullInt64{}
	}
	if id, ok := planPorNombre[strings.TrimSpace(plan.String)]; ok {
		return sql.NullInt64{Int64: int64(id), Valid: true}
	}
	return sql.NullInt64{}
}

// buscarGem devuelve el gem_port_id (= gem-id) de la vlan según gem_config. Si la
// vlan NO está en gem_config, devuelve 0 (no NULL): así en registro_cliente el
// gem_id queda en 0 para las vlan sin mapping. Solo afecta esa columna; el estado
// se decide aparte. Lo usan tanto la 1ª pasada como el repaso.
func buscarGem(vlan int, gemPorVlan map[int]int) sql.NullInt64 {
	if g, ok := gemPorVlan[vlan]; ok {
		return sql.NullInt64{Int64: int64(g), Valid: true}
	}
	return sql.NullInt64{Int64: 0, Valid: true}
}

// descripcion arma el desc del ont add/service-port: nroCliente si existe, si no pppoe.
func descripcion(f FilaCarga) string {
	if f.NroCliente.Valid && f.NroCliente.Int64 != 0 {
		return strings.TrimSpace(sqlIntToStr(f.NroCliente.Int64))
	}
	return f.Pppoe
}

func sqlIntToStr(n int64) string {
	// pequeño helper para evitar importar strconv sólo para esto
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
