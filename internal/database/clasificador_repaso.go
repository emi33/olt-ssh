package database

import (
	"database/sql"
	"sort"
	"strings"
)

// Estados de la segunda pasada (repaso). El prefijo real ("repaso_" o
// "sobrescrito_repaso_") lo agrega ClasificarRepaso según si la posición ya
// había sido cargada por la primera pasada.
const (
	casoSinCliente           = "sin_cliente_en_ct"                     // caso 2
	casoInactivo             = "cliente_inactivo_omitido"              // caso 5
	casoDosMacs              = "onu_dos_macs_dos_clientes"             // caso 1
	casoMultimacParcial      = "onu_multimac_parcial"                  // caso 3 (algunas con cliente)
	casoMultimacSinCliente   = "onu_multimac_sin_cliente"              // caso 3 (ninguna con cliente)
	casoSinMacPlaceholder    = "onu_sin_mac_placeholder"               // caso 4
	casoMixtaRealPlaceholder = "onu_mixta_real_y_placeholder"          // caso 4 (mixto)
	casoSinEqcliente         = "onu_sin_eqcliente"                     // registrada pero sin nada en eqcliente
	casoSnDuplicadoOnuActiva = "sn_duplicado_onu_activa"               // registro viejo: el mismo equipo está en otra posición de ESTA olt, verificada
	casoEquipoTrasladado     = "equipo_trasladado_no_migrar"           // el SN está verificado en OTRA olt: el equipo se mudó, no se migra
	casoVlanCamara           = "vlan_mayor_3000_camara"                // caso 6 sin cliente
	casoVlanCamaraCliente    = "vlan_mayor_3000_con_cliente"           // caso 6 con cliente
	casoVlan1001             = "vlan_1001_aire"                        // caso 9
	casoAusentePatronNo      = "ausente_sin_mac_patron_no_coincide"    // caso 7, sin cliente
	casoAusentePatronOk      = "ausente_sin_mac_patron_ok_sin_cliente" // caso 7, sin cliente
	casoAusenteConCliente    = "ausente_sin_mac_con_cliente"           // caso 7 PERO con cliente: el servicio existe, se migra
	casoMacSinOnu            = "mac_sin_onu_en_puerto"                 // caso 10
	casoMacMultiservicio     = "onu_mac_multiservicio"                 // 1 sola MAC con varias vlans (varios servicios)
	casoEncontradoOtroCt     = "encontrado_en_otro_ct"                 // 1 MAC: no está en los CT configurados pero SÍ en otro CT -> se migra con esos datos
	casoMultimacEnOtroCt     = "onu_multimac_en_otro_ct"               // varias MACs, ninguna en los CT configurados y alguna en otro CT
	casoVariosClientesCt     = "varios_clientes_en_ct"                 // la MAC matcheó varios clientes en CT y se resolvió (1 activo, resto inactivos)
	casoClientesCtAmbiguos   = "clientes_ct_ambiguos"                  // varios clientes en CT SIN un único activo: no se matchea ninguno
	casoVlanSinGem           = "vlan_sin_gem_config"                   // alguna vlan de la ONU no está en gem_config: no hay gem-id para el service-port
	casoListo                = "listo"                                 // 1:1 activo (raro en repaso)
	// casoIgnorar es un centinela interno: la ONU se descarta (no se registra).
	casoIgnorar = "__ignorar__"
)

// casosListos son los casos que se marcan listos para cargar. Además del feliz,
// entran aquellos en los que el cliente EXISTE y lo único que falta se resuelve
// FUERA de este programa:
//
//	casoEncontradoOtroCt  -> el cliente está en un CT que no es de la lista; sus
//	                         datos se copian igual y se migra con ellos.
//	casoInactivo          -> el cliente existe pero está inactivo en el CT.
//	casoAusenteConCliente -> la ONU no está presente en la OLT, pero su servicio
//	                         tiene cliente; que la ONU vuelva se ve en el equipo.
//
// Y los que se migran aunque NO tengan cliente en CT, porque el servicio existe
// igual y se completa en el equipo:
//
//	casoVlanCamara         -> cámara sin cliente en CT: el service-port se crea
//	                          con la vlan de cámara, sin pppoe.
//	casoMultimacSinCliente -> varias MACs sin cliente en ningún CT.
//	casoDosMacs            -> dos MACs con su cliente cada una: los dos servicios
//	                          existen y se migran, uno por fila de cliente.
//
// Marcar el caso NO cambia su clasificación: cada uno conserva su estado y su
// motivo, sólo se les habilita listo_para_cargar.
//
// El equivalente del plan sin reconocer (principal_plan_no_reconocido) vive en
// clasificarFila: ese caso lo produce la 1ª pasada y acá llega re-emitido con su
// listo original.
var casosListos = map[string]bool{
	casoListo:              true,
	casoEncontradoOtroCt:   true,
	casoInactivo:           true,
	casoAusenteConCliente:  true,
	casoVlanCamara:         true,
	casoMultimacSinCliente: true,
	casoDosMacs:            true,
}

// MacInfo es una MAC real de una posición con su cliente resuelto.
type MacInfo struct {
	Mac              string
	Vlan             int
	Idx              sql.NullInt64
	FechaEq          sql.NullString // última fechaHora de la MAC en eqcliente
	TieneCliente     bool           // matcheó cliente en un CT CONFIGURADO (4.5/4.14)
	EnOtroCt         bool           // no está en los CT configurados, pero SÍ en otro CT
	VariosClientesCt bool           // matcheó MÁS de un cliente en CT y se resolvió bien (1 activo, resto inactivos)
	Activo           bool           // el cliente está activo (el elegido, el más reciente)
	// ClientesCtAmbiguos: la MAC matcheó varios clientes en los CT configurados
	// pero NO se cumple "exactamente uno activo y el resto inactivos" (hay 0
	// activos, o 2+). El matcheo no es confiable, así que la MAC queda SIN
	// cliente asignado y la condición se evidencia en el estado de la ONU y de
	// sus filas de cliente.
	ClientesCtAmbiguos bool
	ClientesCtTotal    int // cuántos clientes matchearon en los CT configurados
	ClientesCtActivos  int // de ésos, cuántos con activo=1
	Pppoe              sql.NullString
	Plan               sql.NullString
	NroCliente         sql.NullInt64
	IpCliente          sql.NullString
	IpControlador      sql.NullString
}

// OnuRepaso es una ONU con todo lo necesario para clasificarla en el repaso.
type OnuRepaso struct {
	Puerto          int
	OntID           int
	Sn              sql.NullString
	MacOnu          string // onu.mac (para el patrón sn/mac del caso 7)
	EstadoPresencia string
	CantidadMacs    int
	Macs            []MacInfo    // MACs reales en esta posición
	PlaceholderIdx  []int        // índices con 00:00
	YaPrincipal     bool         // la 1ª pasada ya cargó esta posición
	EstadoPrincipal EstadoPrevio // con qué estado/listo la dejó, para re-emitirla igual
	// DuplicadoMismaOlt y DuplicadoOtraOlt: dónde más está verificado este SN.
	// Los arma ResolverDuplicadoSn sobre el índice de TODAS las OLTs.
	//
	//   DuplicadoMismaOlt -> el equipo sigue en esta OLT, cargado dos veces
	//                        (típicamente con el serial escrito distinto).
	//   DuplicadoOtraOlt  -> el equipo se mudó a otra OLT: no hay que migrarlo.
	DuplicadoMismaOlt *OnuVerificada
	DuplicadoOtraOlt  *OnuVerificada
	SinOnu            bool // hay MAC(s) pero NO existe ONU registrada en esta posición (caso 10)
}

// ResolverMacs convierte las filas crudas de GetMacsHistoricasRepaso —una por
// cada match (servicio × cliente en CT)— en una MacInfo por servicio
// (mac, vlan), agrupadas por posición (puerto, ont_id).
//
// Una misma MAC puede matchear VARIOS clientes en CT: uno activo y otros
// "para borrar". La resolución, en orden:
//
//   - varios en CT CONFIGURADOS sin un único activo -> ambiguo: la MAC queda sin
//     cliente y se marca ClientesCtAmbiguos;
//   - alguno en CT configurado -> gana el activo (ver mejorCliente) y se copian
//     sus datos;
//   - ninguno configurado pero sí en OTRO CT -> se copian igual TODOS sus datos
//     (pppoe, plan, nro de cliente, ips) y se marca EnOtroCt, para poder migrarlo.
//
// El orden de las MacInfo dentro de cada posición sigue el de las filas de
// entrada, para que la salida sea determinística.
func ResolverMacs(macs []MacRepaso, controladores []string) map[[2]int][]MacInfo {
	configurados := map[string]bool{}
	for _, c := range controladores {
		configurados[c] = true
	}

	type clave struct {
		p    [2]int
		mac  string
		vlan int
	}
	type acum struct {
		base        MacInfo   // mac/vlan/idx/fechaEq: invariante del servicio
		confMejor   MacRepaso // mejor cliente en CT configurado
		confTiene   bool
		confCount   int       // clientes en CT configurados
		confActivos int       // de ésos, cuántos con activo=1
		otro        MacRepaso // algún cliente en otro CT
		otroTiene   bool
	}

	acums := map[clave]*acum{}
	var orden []clave
	for _, m := range macs {
		k := clave{[2]int{m.Puerto, m.OntID}, m.Mac, m.Vlan}
		a := acums[k]
		if a == nil {
			a = &acum{base: MacInfo{
				Mac: m.Mac, Vlan: m.Vlan, Idx: m.Idx, FechaEq: m.FechaEq, IpControlador: m.IpControlador,
			}}
			acums[k] = a
			orden = append(orden, k)
		}
		if !m.Pppoe.Valid {
			continue // este match no trajo cliente
		}
		if configurados[nn(m.IpControlador)] {
			a.confCount++
			if m.Activo.Valid && m.Activo.Int64 == 1 {
				a.confActivos++
			}
			if !a.confTiene || mejorCliente(m, a.confMejor) {
				a.confMejor = m
				a.confTiene = true
			}
		} else if !a.otroTiene {
			a.otro = m
			a.otroTiene = true
		}
	}

	out := map[[2]int][]MacInfo{}
	for _, k := range orden {
		a := acums[k]
		mi := a.base
		mi.ClientesCtTotal = a.confCount
		mi.ClientesCtActivos = a.confActivos
		switch {
		case a.confCount > 1 && a.confActivos != 1:
			// Sin un único activo no hay forma confiable de saber cuál es el
			// vigente: la MAC queda SIN cliente y se evidencia en el estado.
			mi.ClientesCtAmbiguos = true
		case a.confTiene:
			mi.TieneCliente = true
			mi.Activo = a.confMejor.Activo.Valid && a.confMejor.Activo.Int64 == 1
			mi.VariosClientesCt = a.confCount > 1 // varios, pero resueltos (1 activo)
			mi.Pppoe = a.confMejor.Pppoe
			mi.Plan = a.confMejor.Plan
			mi.NroCliente = a.confMejor.NroCliente
			mi.IpCliente = a.confMejor.IpCliente
			mi.IpControlador = a.confMejor.IpControlador
		case a.otroTiene:
			// Cliente en un CT fuera de la lista: se copia TODO igual para poder
			// migrarlo; lo que ese CT no traiga queda vacío.
			mi.EnOtroCt = true
			mi.Pppoe = a.otro.Pppoe
			mi.Plan = a.otro.Plan
			mi.NroCliente = a.otro.NroCliente
			mi.IpCliente = a.otro.IpCliente
			mi.IpControlador = a.otro.IpControlador
		}
		out[k.p] = append(out[k.p], mi)
	}
	return out
}

// mejorCliente decide, entre dos matches de la MISMA MAC en un CT configurado,
// cuál conservar: SIEMPRE gana el activo (activo=1) — es el cliente vigente, el
// resto está en 0 (para borrar). Si ninguno o ambos están activos, desempata por
// ct.fecha más nueva. La comparación de fechas es textual: ct.fecha viene en
// formato ISO, que ordena igual lexicográfica que cronológicamente.
func mejorCliente(nuevo, actual MacRepaso) bool {
	na := nuevo.Activo.Valid && nuevo.Activo.Int64 == 1
	aa := actual.Activo.Valid && actual.Activo.Int64 == 1
	if na != aa {
		return na
	}
	return nn(nuevo.FechaCt) > nn(actual.FechaCt)
}

// NormalizarSn deja el SN en una forma comparable entre las distintas
// convenciones de escritura. Distintos equipos/cargas escriben el mismo serial
// como "vendor+mac" o "vendor-mac" (p. ej. "TPLGF58E9CB8" y "TPLG-F58E9CB8"), y
// eso hace que el MISMO equipo físico aparezca como dos ONUs distintas.
//
// Se pasa a mayúsculas y se descarta todo lo que no sea alfanumérico, así el
// criterio no depende de cuál separador se haya usado.
func NormalizarSn(sn string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(sn)) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// IndexarVerificadasPorSn agrupa las ONUs verificadas de TODAS las OLTs por SN
// normalizado. La clave normalizada es lo que permite reconocer al mismo equipo
// aunque cada OLT escriba el serial con su propia convención (con guión o sin
// él). Descarta los SN que no normalizan a nada.
func IndexarVerificadasPorSn(onus []OnuVerificada) map[string][]OnuVerificada {
	idx := map[string][]OnuVerificada{}
	for _, o := range onus {
		if clave := NormalizarSn(o.Sn); clave != "" {
			idx[clave] = append(idx[clave], o)
		}
	}
	return idx
}

// ResolverDuplicadoSn separa las apariciones verificadas de un SN en las dos que
// importan para una posición dada:
//
//   - mismaOlt: otra posición de ESTA misma OLT con el mismo SN. El equipo sigue
//     acá, cargado dos veces (típicamente con el serial escrito distinto).
//   - otraOlt: una posición verificada en OTRA OLT. El equipo se mudó: ya no está
//     en esta OLT y no hay que migrarlo.
//
// La propia posición se excluye siempre. Ante varios candidatos se elige el menor
// por (idOlt, puerto, ont) para que el resultado no dependa del orden en que la
// base devuelva las filas.
func ResolverDuplicadoSn(refs []OnuVerificada, idOlt, puerto, ontID int) (mismaOlt, otraOlt *OnuVerificada) {
	for i := range refs {
		r := refs[i]
		if r.IDOlt == idOlt {
			if r.Puerto == puerto && r.OntID == ontID {
				continue // es esta misma posición
			}
			if mismaOlt == nil || menorUbicacion(r, *mismaOlt) {
				c := r
				mismaOlt = &c
			}
			continue
		}
		if otraOlt == nil || menorUbicacion(r, *otraOlt) {
			c := r
			otraOlt = &c
		}
	}
	return mismaOlt, otraOlt
}

// menorUbicacion ordena por (idOlt, puerto, ont) para desempatar de forma estable.
func menorUbicacion(a, b OnuVerificada) bool {
	if a.IDOlt != b.IDOlt {
		return a.IDOlt < b.IDOlt
	}
	if a.Puerto != b.Puerto {
		return a.Puerto < b.Puerto
	}
	return a.OntID < b.OntID
}

// ClasificarRepaso evalúa una ONU COMPLETA (todas sus MACs) y devuelve la fila
// registro_onu y las registro_cliente listas para escribir, con estado y cascada
// (ONU y todos sus clientes comparten el estado). skip=true sólo para las ONUs
// que no se registran nunca (ausente_sin_sn). Aplica el prefijo
// repaso_/sobrescrito_repaso_.
func ClasificarRepaso(o OnuRepaso, idOlt int, planPorNombre map[string]int, gemPorVlan map[int]int) (onu RegistroOnu, clientes []RegistroCliente, skip bool) {
	base, motivo := clasificarOnuRepaso(o, gemPorVlan)

	// ausente_sin_sn: se ignora por completo (tenga o no MACs).
	if base == casoIgnorar {
		return RegistroOnu{}, nil, true
	}

	prefijo := "repaso_"
	if o.YaPrincipal {
		prefijo = "sobrescrito_repaso_"
	}
	estado := prefijo + base
	listo := casosListos[base]

	// Caso feliz simple que la 1ª pasada ya había cargado: se re-emite IDÉNTICO,
	// con el estado y el listo que ESA pasada le puso, en lugar de saltearlo. El
	// repaso vacía y reescribe las tablas de la OLT en una transacción, así que lo
	// que no se emite se pierde: emitirlo tal cual es lo que antes conseguía el
	// salteo.
	//
	// Se copia el estado previo en vez de asumir 'principal_listo' porque el
	// repaso llega hasta acá sin haber validado el plan (casoListo sólo mira
	// TieneCliente && Activo) y porque YaPrincipal matchea todo 'principal_%',
	// incluidos los estados que la 1ª pasada rechazó — asumir listo los promovería
	// indebidamente a "listo para cargar".
	if base == casoListo && o.YaPrincipal && o.EstadoPrincipal.Estado != "" {
		estado, listo = o.EstadoPrincipal.Estado, o.EstadoPrincipal.Listo
	}

	// El plan sin reconocer NO bloquea la carga: el cliente existe y su servicio
	// hay que darlo de alta; el plan se carga a mano en el equipo y el
	// service-port se crea sin traffic-in/out (ver cmd/provisionar). Se fuerza acá
	// —después del copiado de arriba— porque corridas viejas dejaron estas filas
	// con listo_para_cargar=0 y el copiado del estado previo las perpetuaba
	// corrida tras corrida.
	if EsPlanNoReconocido(estado) {
		listo = true
	}

	// Los detalles de los índices 00:00 y de las vlans multi-servicio se agregan
	// siempre, sea cual sea el caso.
	motivo = unirMotivos(motivo, motivoMultiservicio(o.Macs), motivoPlaceholders(o.PlaceholderIdx))

	sn := snParaRegistro(o.Sn)

	onu = RegistroOnu{
		IDOlt:           idOlt,
		Puerto:          o.Puerto,
		OntID:           o.OntID,
		Sn:              sn,
		EstadoPresencia: sql.NullString{String: o.EstadoPresencia, Valid: o.EstadoPresencia != ""},
		CantidadMacs:    o.CantidadMacs,
		DescOnt:         "",
		Estado:          estado,
		Listo:           listo,
		Motivo:          motivo,
	}

	// Una fila cliente por MAC real (cascada: todas con el estado de la ONU).
	for _, m := range o.Macs {
		clientes = append(clientes, RegistroCliente{
			IDOlt:         idOlt,
			Puerto:        o.Puerto,
			OntID:         o.OntID,
			MacWan:        m.Mac,
			Pppoe:         nn(m.Pppoe),
			Vlan:          m.Vlan,
			PlanID:        buscarPlan(m.Plan, planPorNombre),
			Plan:          nn(m.Plan),
			GemID:         buscarGem(m.Vlan, gemPorVlan),
			Idx:           m.Idx,
			FechaEq:       m.FechaEq,
			NroCliente:    m.NroCliente,
			IpCliente:     m.IpCliente,
			IpControlador: m.IpControlador,
			Estado:        estado,
			Listo:         listo,
			Motivo:        motivoMac(m),
		})
	}
	return onu, clientes, false
}

// motivoMac describe, a nivel de fila cliente, por qué esa MAC quedó como quedó.
// Sólo se llena cuando hubo algo raro con su matcheo en CT: así la evidencia
// queda tanto en registro_onu (estado + motivo de la ONU) como en la fila exacta
// de registro_cliente que la provocó.
func motivoMac(m MacInfo) string {
	switch {
	case m.ClientesCtAmbiguos:
		return "sin cliente: " + sqlIntToStr(int64(m.ClientesCtTotal)) + " clientes en CT, " +
			sqlIntToStr(int64(m.ClientesCtActivos)) + " activos"
	case m.VariosClientesCt:
		return "elegido entre " + sqlIntToStr(int64(m.ClientesCtTotal)) + " clientes en CT (1 activo)"
	case m.EnOtroCt:
		return "cliente tomado del ct " + nn(m.IpControlador) + " (fuera de los configurados)"
	default:
		return ""
	}
}

// vlansSinGem devuelve las vlans de la ONU que NO están en gem_config, sin
// repetir y ordenadas. Una vlan ausente ahí no tiene gem-id, y sin gem-id no se
// puede armar el 'service-port'.
func vlansSinGem(macs []MacInfo, gemPorVlan map[int]int) []int {
	vistas := map[int]bool{}
	var out []int
	for _, m := range macs {
		if _, tiene := gemPorVlan[m.Vlan]; tiene || vistas[m.Vlan] {
			continue
		}
		vistas[m.Vlan] = true
		out = append(out, m.Vlan)
	}
	sort.Ints(out)
	return out
}

// clasificarOnuRepaso decide el caso base (sin prefijo) y un motivo opcional.
func clasificarOnuRepaso(o OnuRepaso, gemPorVlan map[int]int) (base, motivo string) {
	// Caso 10: hay MAC(s) pero la posición no tiene ONU registrada en `onu`.
	if o.SinOnu {
		return casoMacSinOnu, ""
	}
	// ausente_sin_sn: la ONU NO existe registrada (solo historial en la BD). No
	// se carga ni se buscan sus macs, tenga o no tráfico.
	if strings.EqualFold(strings.TrimSpace(o.EstadoPresencia), "ausente_sin_sn") {
		return casoIgnorar, ""
	}
	// Caso 7: ausente_sin_mac (la ONU trae una MAC vieja de una corrida anterior).
	//
	// Se mira PRIMERO si la posición tiene cliente. Los dos estados de abajo
	// terminan en "_sin_cliente" y eso hay que verificarlo, no asumirlo: que la
	// ONU no esté presente en la OLT no significa que no haya servicio. Si hay un
	// cliente identificado, el servicio existe y hay que migrarlo igual — que la
	// ONU vuelva a aparecer se resuelve en el equipo, no acá.
	if strings.EqualFold(strings.TrimSpace(o.EstadoPresencia), "ausente_sin_mac") {
		if algunaConCliente(o.Macs) {
			return casoAusenteConCliente, "onu ausente en la olt pero con cliente; mac vieja: " + o.MacOnu
		}
		if macCoincideConSn(o.Sn.String, o.MacOnu) {
			return casoAusentePatronOk, "mac vieja: " + o.MacOnu
		}
		return casoAusentePatronNo, "mac vieja: " + o.MacOnu
	}

	n := len(o.Macs)
	tienePh := len(o.PlaceholderIdx) > 0

	// Matcheo no confiable: alguna MAC tiene varios clientes en los CT
	// configurados sin cumplir "exactamente uno activo, el resto inactivos". No
	// se le asignó cliente a esa MAC, así que cualquier conclusión sobre "con
	// cliente / sin cliente" sería falsa: la ONU entera queda marcada para
	// revisión. Va ANTES que cámara y multi-MAC por eso mismo.
	if amb := macsAmbiguas(o.Macs); len(amb) > 0 {
		return casoClientesCtAmbiguos, detalleAmbiguas(amb)
	}

	// Vlan sin gem_config: sin gem-id no se puede armar el service-port, así que
	// la ONU no es provisionable tal como está, sea cual sea el resto de su
	// situación. Es una regla GENERAL y por eso va por encima de cámara, de
	// multi-MAC y del caso feliz. Sólo cede ante los casos de arriba, que son
	// problemas de integridad de datos más básicos.
	if sinGem := vlansSinGem(o.Macs, gemPorVlan); len(sinGem) > 0 {
		return casoVlanSinGem, "vlans sin gem_config: " + joinInts(sinGem)
	}

	// Cámara (vlan>3000) a nivel ONU, con PRIORIDAD sobre multi-MAC: si alguna
	// MAC del puerto es de cámara, toda la ONU (y sus clientes) se marca así.
	if macsConVlanCamara(o.Macs) {
		if contarConCliente(o.Macs) > 0 {
			return casoVlanCamaraCliente, ""
		}
		return casoVlanCamara, ""
	}

	// Las ramas multi-MAC cuentan MACs DISTINTAS, no filas: varias MACs son varios
	// equipos (ambiguo a quién provisionar), mientras que una MAC con varias vlans
	// es UN equipo con varios servicios, que es normal y tiene su propio caso.
	distintas := macsDistintas(o.Macs)

	switch {
	case n == 0 && tienePh:
		return casoSinMacPlaceholder, ""
	case n == 0:
		// Registrada en `onu` pero sin UNA SOLA fila en eqcliente: nunca pasó
		// tráfico en esta posición. Si el mismo SN aparece verificado en otro
		// lado, esta fila es un registro que sobró. Ninguno de los dos casos es
		// casoListo, así que listo_para_cargar queda en 0 y nunca se suben.
		switch {
		case o.DuplicadoMismaOlt != nil:
			// El equipo sigue en esta OLT, cargado dos veces. Tiene prioridad
			// sobre el traslado: si está verificado ACÁ, acá está.
			d := o.DuplicadoMismaOlt
			return casoSnDuplicadoOnuActiva, "mismo equipo que " +
				sqlIntToStr(int64(d.Puerto)) + "/" + sqlIntToStr(int64(d.OntID)) +
				" (sn " + d.Sn + "), que está verificada"
		case o.DuplicadoOtraOlt != nil:
			// El equipo está verificado en OTRA OLT: se mudó. No se migra.
			d := o.DuplicadoOtraOlt
			return casoEquipoTrasladado, "verificado en olt " + sqlIntToStr(int64(d.IDOlt)) +
				" puerto " + sqlIntToStr(int64(d.Puerto)) + "/" + sqlIntToStr(int64(d.OntID)) +
				" (sn " + d.Sn + "): equipo trasladado, no migrar"
		}
		return casoSinEqcliente, ""
	case distintas >= 2:
		con := macsDistintasConCliente(o.Macs)
		var caso string
		switch {
		case con == distintas:
			if distintas == 2 {
				caso = casoDosMacs
			} else {
				caso = casoMultimacParcial // varias con cliente (excepción igual)
			}
		case con == 0:
			// Ninguna con cliente en los CT configurados: si alguna está en OTRO
			// CT, se marca así; si no, es multi-mac sin cliente. Estado propio y
			// NO listo: acá la ambigüedad es cuál de las MACs corresponde al
			// cliente, no de qué controlador salieron los datos.
			if algunaEnOtroCt(o.Macs) {
				caso = casoMultimacEnOtroCt
			} else {
				caso = casoMultimacSinCliente
			}
		default:
			caso = casoMultimacParcial
		}
		return caso, ""
	case n >= 2:
		// Una sola MAC pero varias filas: el mismo equipo con varios servicios.
		return casoMacMultiservicio, ""
	default: // n == 1
		m := o.Macs[0]
		switch {
		case m.Vlan == 1001:
			return casoVlan1001, ""
		case m.Vlan > 3000:
			if m.TieneCliente {
				return casoVlanCamaraCliente, ""
			}
			return casoVlanCamara, ""
		case tienePh:
			return casoMixtaRealPlaceholder, ""
		case m.TieneCliente && m.VariosClientesCt:
			// Varios clientes en CT. La elección es confiable cuando se cumplen
			// las tres condiciones: hubo EXACTAMENTE un activo, el elegido es ese
			// activo, y su pppoe efectivamente quedó en la fila de cliente. En ese
			// caso el resultado no es ambiguo y la ONU va como listo; el detalle
			// de que hubo varios candidatos queda en el motivo (de la ONU y de la
			// fila cliente), no en el estado.
			if m.ClientesCtActivos == 1 && m.Activo && nn(m.Pppoe) != "" {
				return casoListo, "resuelto entre " + sqlIntToStr(int64(m.ClientesCtTotal)) +
					" clientes en CT (1 activo): " + nn(m.Pppoe)
			}
			// No se pudo confirmar el activo o quedó sin pppoe: se marca distinto
			// para NO confundir la ONU con un listo para subir.
			return casoVariosClientesCt, ""
		case m.TieneCliente && m.Activo:
			return casoListo, ""
		case m.TieneCliente && !m.Activo:
			return casoInactivo, ""
		case m.EnOtroCt:
			// Sin cliente en los CT configurados, pero la MAC aparece en otro CT.
			// Se migra con los datos de ese controlador; el motivo deja registrado
			// de dónde salieron, porque no es uno de los CT de la lista.
			return casoEncontradoOtroCt, "datos tomados del ct " + nn(m.IpControlador) +
				" (fuera de los configurados)"
		default:
			return casoSinCliente, ""
		}
	}
}

// DedupMacs resuelve las entradas repetidas DENTRO de una misma posición
// (puerto, ont_id). Dos entradas conviven sólo si son servicios genuinamente
// distintos; en cualquier otro caso se conserva la más reciente (mayor fechaHora
// de eqcliente) y la anterior se descarta:
//
//	misma mac + misma vlan + mismo idx    -> queda la última (la anterior es historial)
//	misma mac + misma vlan + distinto idx -> queda la última (la OLT le reasignó el índice)
//	cualquier mac + mismo idx             -> queda la última (un índice = una boca)
//	misma mac + distinta vlan + distinto idx -> SE CONSERVAN LAS DOS (dos servicios)
//
// O sea: se colapsa por (mac, vlan) y también por idx. Las entradas con idx NULL
// no compiten por índice (no hay nada que comparar) y sólo se colapsan por
// (mac, vlan). Devuelve las conservadas en el orden de entrada y las descartadas,
// para poder reportarlas.
//
// La comparación de fechas es textual: fechaHora viene en formato ISO
// (YYYY-MM-DD HH:MM:SS), que ordena igual lexicográfica que cronológicamente.
// Ante empate exacto gana la primera, para que el resultado sea determinístico.
func DedupMacs(macs []MacInfo) (conservadas, descartadas []MacInfo) {
	type claveServicio struct {
		mac  string
		vlan int
	}
	porServicio := map[claveServicio]int{} // (mac,vlan) -> posición en conservadas
	porIdx := map[int64]int{}              // idx        -> posición en conservadas

	// reemplazar deja en `conservadas[i]` la entrada más reciente entre la que ya
	// estaba y la nueva, y manda la otra a descartadas.
	reemplazar := func(i int, m MacInfo) {
		if nn(m.FechaEq) > nn(conservadas[i].FechaEq) {
			descartadas = append(descartadas, conservadas[i])
			conservadas[i] = m
		} else {
			descartadas = append(descartadas, m)
		}
		// La ranura i quedó con la entrada ganadora, que puede tener otra mac/vlan
		// u otro idx que la que estaba: hay que registrar SUS claves para que una
		// entrada posterior colisione contra ella. Las claves de la perdedora
		// siguen apuntando a esta misma ranura, que es lo correcto — si vuelve a
		// aparecer, tiene que colapsar acá igual.
		g := conservadas[i]
		porServicio[claveServicio{g.Mac, g.Vlan}] = i
		if g.Idx.Valid {
			porIdx[g.Idx.Int64] = i
		}
	}

	for _, m := range macs {
		ks := claveServicio{m.Mac, m.Vlan}
		if i, choca := porServicio[ks]; choca {
			reemplazar(i, m)
			continue
		}
		if m.Idx.Valid {
			if i, choca := porIdx[m.Idx.Int64]; choca {
				reemplazar(i, m)
				continue
			}
		}
		porServicio[ks] = len(conservadas)
		if m.Idx.Valid {
			porIdx[m.Idx.Int64] = len(conservadas)
		}
		conservadas = append(conservadas, m)
	}
	return conservadas, descartadas
}

// PlaceholdersLibres filtra los índices con MAC 00:00 de una posición y deja sólo
// los que NO están ocupados por una MAC real de esa misma posición. Un índice con
// tráfico real y además un 00:00 es la foto vieja del mismo índice: el 00:00 no
// aporta nada y sólo infla el listado. (Los idx = 0 los descarta antes la
// consulta, ver GetPlaceholderIdx.)
func PlaceholdersLibres(idxs []int, macs []MacInfo) []int {
	if len(idxs) == 0 {
		return nil
	}
	ocupados := map[int64]bool{}
	for _, m := range macs {
		if m.Idx.Valid {
			ocupados[m.Idx.Int64] = true
		}
	}
	var libres []int
	for _, i := range idxs {
		if !ocupados[int64(i)] {
			libres = append(libres, i)
		}
	}
	return libres
}

// macsAmbiguas devuelve las MACs cuyo matcheo contra CT no es confiable (varios
// clientes sin un único activo).
func macsAmbiguas(macs []MacInfo) []MacInfo {
	var out []MacInfo
	for _, m := range macs {
		if m.ClientesCtAmbiguos {
			out = append(out, m)
		}
	}
	return out
}

// detalleAmbiguas arma el motivo de la ONU: qué MAC y con cuántos clientes.
func detalleAmbiguas(macs []MacInfo) string {
	partes := make([]string, 0, len(macs))
	for _, m := range macs {
		partes = append(partes, m.Mac+" ("+sqlIntToStr(int64(m.ClientesCtTotal))+" clientes, "+
			sqlIntToStr(int64(m.ClientesCtActivos))+" activos)")
	}
	return "sin un unico activo: " + strings.Join(partes, "; ")
}

// motivoPlaceholders describe los índices con MAC 00:00 que quedaron tras el
// filtrado, para que se vean en registro_onu.
func motivoPlaceholders(idxs []int) string {
	if len(idxs) == 0 {
		return ""
	}
	return "indices_00: " + joinInts(idxs)
}

// unirMotivos concatena los motivos no vacíos con " | ", recortando a los 255
// caracteres que admite la columna.
func unirMotivos(partes ...string) string {
	vivos := make([]string, 0, len(partes))
	for _, p := range partes {
		if p != "" {
			vivos = append(vivos, p)
		}
	}
	m := strings.Join(vivos, " | ")
	if len(m) > 255 {
		m = m[:255]
	}
	return m
}

// algunaConCliente indica si alguna MAC de la posición tiene un cliente
// identificado, sea en un CT configurado o en otro. Las MACs con matcheo ambiguo
// NO cuentan: ahí justamente no se pudo determinar el cliente.
func algunaConCliente(macs []MacInfo) bool {
	for _, m := range macs {
		if m.TieneCliente || m.EnOtroCt {
			return true
		}
	}
	return false
}

// algunaEnOtroCt indica si alguna MAC fue hallada en un CT no configurado.
func algunaEnOtroCt(macs []MacInfo) bool {
	for _, m := range macs {
		if m.EnOtroCt {
			return true
		}
	}
	return false
}

// macsConVlanCamara indica si alguna MAC de la ONU tiene vlan de cámara (>3000).
func macsConVlanCamara(macs []MacInfo) bool {
	for _, m := range macs {
		if vlanEsCamara(m.Vlan) {
			return true
		}
	}
	return false
}

func contarConCliente(macs []MacInfo) (n int) {
	for _, m := range macs {
		if m.TieneCliente && m.Activo {
			n++
		}
	}
	return n
}

// macsDistintas cuenta equipos, no servicios: una MAC con tres vlans es UNA MAC.
func macsDistintas(macs []MacInfo) int {
	vistas := map[string]bool{}
	for _, m := range macs {
		vistas[m.Mac] = true
	}
	return len(vistas)
}

// macsDistintasConCliente cuenta las MACs con AL MENOS un servicio con cliente
// activo. Es el numerador que se compara contra macsDistintas en las ramas
// multi-MAC.
func macsDistintasConCliente(macs []MacInfo) int {
	vistas := map[string]bool{}
	for _, m := range macs {
		if m.TieneCliente && m.Activo {
			vistas[m.Mac] = true
		}
	}
	return len(vistas)
}

// motivoMultiservicio lista las vlans cuando alguna MAC presta más de un
// servicio. Se agrega en CUALQUIER caso, no sólo en casoMacMultiservicio: una MAC
// con internet + cámara clasifica como cámara (que tiene prioridad), y sin esto
// se perdería de vista que ahí hay dos servicios.
func motivoMultiservicio(macs []MacInfo) string {
	if len(macs) == 0 || len(macs) == macsDistintas(macs) {
		return "" // ninguna MAC repetida: un servicio por equipo
	}
	vistas := map[int]bool{}
	vlans := make([]int, 0, len(macs))
	for _, m := range macs {
		if !vistas[m.Vlan] {
			vistas[m.Vlan] = true
			vlans = append(vlans, m.Vlan)
		}
	}
	return "vlans: " + joinInts(vlans)
}

// macCoincideConSn compara los últimos 6 caracteres de sn y mac (los 5 primeros
// iguales, el 6º distinto). Mismo criterio que read-olt para las ONUs Kingtype.
func macCoincideConSn(sn, mac string) bool {
	sn = strings.ToUpper(strings.TrimSpace(sn))
	mac = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(mac), ":", ""))
	if len(sn) < 6 || len(mac) < 6 {
		return false
	}
	sufSn := sn[len(sn)-6:]
	sufMac := mac[len(mac)-6:]
	return sufSn[:5] == sufMac[:5] && sufSn[5] != sufMac[5]
}

func nn(s sql.NullString) string {
	if s.Valid {
		return s.String
	}
	return ""
}

func joinInts(xs []int) string {
	sort.Ints(xs)
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = sqlIntToStr(int64(x))
	}
	return strings.Join(parts, ",")
}
