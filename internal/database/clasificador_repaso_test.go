package database

import (
	"database/sql"
	"strings"
	"testing"
)

func macReal(mac string, vlan int, conCliente, activo bool) MacInfo {
	mi := MacInfo{Mac: mac, Vlan: vlan, Idx: ni(1)}
	if conCliente {
		mi.TieneCliente = true
		mi.Activo = activo
		mi.Pppoe = ns("cli/1")
		mi.Plan = ns("100 MB Hogar")
	}
	return mi
}

func TestClasificarRepaso(t *testing.T) {
	planes := map[string]int{"100 MB Hogar": 1}
	// gem refleja las vlans que gem_config tiene de verdad: las de cliente, las de
	// cámara y la de aire. Una vlan que NO esté acá dispara casoVlanSinGem, así
	// que el fixture tiene que ser realista o los otros casos no se pueden probar.
	gem := map[int]int{
		20: 1, 40: 1, 50: 1, 65: 1, 75: 1, 102: 3, 108: 3, 110: 3,
		600: 2, 620: 2, 630: 2, 1001: 2,
		3990: 2, 3991: 2, 3996: 2, 4000: 2,
	}

	casos := []struct {
		nombre       string
		o            OnuRepaso
		esperado     string // estado esperado (con prefijo)
		esperaSkip   bool
		esperaNClien int
		esperaListo  bool
		esperaMotivo string // subcadena que debe aparecer en el motivo de la ONU
	}{
		{
			// Ya no se saltea: se re-emite idéntico, porque la tabla se vacía y se
			// reescribe entera y lo que no se emite se perdería.
			nombre: "feliz ya cargado por 1ª pasada -> se re-emite como principal_listo",
			o: OnuRepaso{Puerto: 9, OntID: 9, EstadoPresencia: "mac_verificada", YaPrincipal: true,
				EstadoPrincipal: EstadoPrevio{Estado: EstadoListo, Listo: true},
				Macs:            []MacInfo{macReal("AA", 65, true, true)}},
			esperado:     EstadoListo,
			esperaNClien: 1,
			esperaListo:  true,
		},
		{
			// El repaso conserva el estado, y plan_no_reconocido va siempre listo:
			// el plan se carga a mano en la OLT y el service-port se crea sin
			// traffic-in/out.
			nombre: "ya cargado como plan_no_reconocido -> se conserva y sigue listo",
			o: OnuRepaso{Puerto: 9, OntID: 10, EstadoPresencia: "mac_verificada", YaPrincipal: true,
				EstadoPrincipal: EstadoPrevio{Estado: EstadoPlanNoReconocido, Listo: true},
				Macs:            []MacInfo{macReal("AA", 65, true, true)}},
			esperado:     EstadoPlanNoReconocido,
			esperaNClien: 1,
			esperaListo:  true,
		},
		{
			// plan_no_reconocido es la EXCEPCIÓN a "el repaso no promueve lo que la
			// 1ª pasada rechazó": corridas viejas lo dejaron en listo=0 y el copiado
			// del estado previo lo perpetuaba, así que acá se fuerza a listo=1.
			nombre: "plan_no_reconocido con listo=0 -> el repaso SÍ lo promueve",
			o: OnuRepaso{Puerto: 9, OntID: 15, EstadoPresencia: "mac_verificada", YaPrincipal: true,
				EstadoPrincipal: EstadoPrevio{Estado: EstadoPlanNoReconocido, Listo: false},
				Macs:            []MacInfo{macReal("AA", 65, true, true)}},
			esperado:     EstadoPlanNoReconocido,
			esperaNClien: 1,
			esperaListo:  true,
		},
		{
			nombre: "ya cargado como estado_no_cargable -> se conserva",
			o: OnuRepaso{Puerto: 9, OntID: 11, EstadoPresencia: "mac_verificada", YaPrincipal: true,
				EstadoPrincipal: EstadoPrevio{Estado: EstadoNoCargable, Listo: false},
				Macs:            []MacInfo{macReal("AA", 65, true, true)}},
			esperado:     EstadoNoCargable,
			esperaNClien: 1,
			esperaListo:  false,
		},
		{
			// Defensivo: YaPrincipal sin estado previo cargado no debe promover.
			nombre:       "YaPrincipal sin estado previo -> sobrescrito_repaso_listo, sin promover",
			o:            OnuRepaso{Puerto: 9, OntID: 12, EstadoPresencia: "mac_verificada", YaPrincipal: true, Macs: []MacInfo{macReal("AA", 65, true, true)}},
			esperado:     "sobrescrito_repaso_listo",
			esperaNClien: 1,
			esperaListo:  true,
		},
		{
			nombre:       "1 mac sin cliente -> sin_cliente_en_ct",
			o:            OnuRepaso{Puerto: 1, OntID: 1, EstadoPresencia: "mac_verificada", Macs: []MacInfo{macReal("AA", 65, false, false)}},
			esperado:     "repaso_sin_cliente_en_ct",
			esperaNClien: 1,
		},
		{
			// El cliente existe, sólo está inactivo en el CT: se marca aparte pero
			// va listo igual.
			nombre:       "1 mac cliente inactivo -> se marca, pero va listo",
			o:            OnuRepaso{Puerto: 1, OntID: 2, EstadoPresencia: "mac_verificada", Macs: []MacInfo{macReal("AA", 65, true, false)}},
			esperado:     "repaso_cliente_inactivo_omitido",
			esperaNClien: 1,
			esperaListo:  true,
		},
		{
			nombre:       "2 macs ambas con cliente -> caso 1, sobrescrito",
			o:            OnuRepaso{Puerto: 6, OntID: 40, EstadoPresencia: "macs_multiples", YaPrincipal: true, Macs: []MacInfo{macReal("AA", 65, true, true), macReal("BB", 65, true, true)}},
			esperado:     "sobrescrito_repaso_onu_dos_macs_dos_clientes",
			esperaNClien: 2,
			esperaListo:  true,
		},
		{
			nombre:       "multimac ninguna con cliente",
			o:            OnuRepaso{Puerto: 7, OntID: 3, EstadoPresencia: "macs_multiples", Macs: []MacInfo{macReal("AA", 65, false, false), macReal("BB", 65, false, false)}},
			esperado:     "repaso_onu_multimac_sin_cliente",
			esperaNClien: 2,
			esperaListo:  true,
		},
		{
			// El mismo equipo está en 5/12 de ESTA olt, con el SN escrito con guión
			// y verificada: esta fila es el registro viejo y nunca debe subirse.
			nombre: "sin_eqcliente con SN duplicado en la misma olt -> nunca subir",
			o: OnuRepaso{Puerto: 5, OntID: 40, EstadoPresencia: "sin_trafico", Sn: ns("TPLGF58E9CB8"),
				DuplicadoMismaOlt: &OnuVerificada{IDOlt: 4, Puerto: 5, OntID: 12, Sn: "TPLG-F58E9CB8"}},
			esperado:     "repaso_sn_duplicado_onu_activa",
			esperaNClien: 0,
			esperaMotivo: "mismo equipo que 5/12 (sn TPLG-F58E9CB8), que está verificada",
		},
		{
			// El equipo está verificado en la olt 7: se mudó, no hay que migrarlo.
			nombre: "sin_eqcliente con SN verificado en OTRA olt -> equipo trasladado",
			o: OnuRepaso{Puerto: 5, OntID: 43, EstadoPresencia: "sin_trafico", Sn: ns("TPLGB76EFC58"),
				DuplicadoOtraOlt: &OnuVerificada{IDOlt: 7, Puerto: 2, OntID: 15, Sn: "TPLG-B76EFC58"}},
			esperado:     "repaso_equipo_trasladado_no_migrar",
			esperaNClien: 0,
			esperaMotivo: "verificado en olt 7 puerto 2/15 (sn TPLG-B76EFC58): equipo trasladado, no migrar",
		},
		{
			// Datos contradictorios: verificado acá Y en otra olt. Gana "acá",
			// porque si está verificado en esta olt, el equipo está en esta olt.
			nombre: "sin_eqcliente duplicado en las dos -> gana la misma olt",
			o: OnuRepaso{Puerto: 5, OntID: 44, EstadoPresencia: "sin_trafico", Sn: ns("TPLGB76EFC58"),
				DuplicadoMismaOlt: &OnuVerificada{IDOlt: 4, Puerto: 5, OntID: 12, Sn: "TPLG-B76EFC58"},
				DuplicadoOtraOlt:  &OnuVerificada{IDOlt: 7, Puerto: 2, OntID: 15, Sn: "TPLG-B76EFC58"}},
			esperado:     "repaso_sn_duplicado_onu_activa",
			esperaNClien: 0,
		},
		{
			nombre: "sin_eqcliente SIN duplicado -> sigue siendo onu_sin_eqcliente",
			o: OnuRepaso{Puerto: 5, OntID: 41, EstadoPresencia: "sin_trafico",
				Sn: ns("TPLGF58E9CB8")},
			esperado:     "repaso_onu_sin_eqcliente",
			esperaNClien: 0,
		},
		{
			// Regla general: sin gem-id no hay service-port posible, así que pisa
			// al caso feliz aunque el cliente esté perfecto.
			nombre: "vlan fuera de gem_config pisa al caso listo",
			o: OnuRepaso{Puerto: 20, OntID: 10, EstadoPresencia: "mac_verificada",
				Macs: []MacInfo{macReal("AA", 1280, true, true)}},
			esperado:     "repaso_vlan_sin_gem_config",
			esperaNClien: 1,
			esperaMotivo: "vlans sin gem_config: 1280",
		},
		{
			// El caso real de producción: pto 3 / ont 1, con vlans 1, 1280 y 1536
			// fuera de gem_config. Antes caía en multimac_parcial.
			nombre: "multimac con vlans fuera de gem_config -> vlan_sin_gem_config",
			o: OnuRepaso{Puerto: 20, OntID: 11, EstadoPresencia: "macs_multiples", Macs: []MacInfo{
				macReal("AA", 1280, false, false), macReal("BB", 1536, false, false),
				macReal("CC", 1, false, false), macReal("DD", 65, true, true)}},
			esperado:     "repaso_vlan_sin_gem_config",
			esperaNClien: 4,
			esperaMotivo: "vlans sin gem_config: 1,1280,1536",
		},
		{
			// Cámara con las vlans bien cargadas: sigue como cámara.
			nombre: "camara con vlans en gem_config -> sigue siendo camara",
			o: OnuRepaso{Puerto: 20, OntID: 12, EstadoPresencia: "macs_multiples", Macs: []MacInfo{
				macReal("AA", 4000, false, false), macReal("BB", 3996, false, false)}},
			esperado:     "repaso_vlan_mayor_3000_camara",
			esperaNClien: 2,
			esperaListo:  true,
		},
		{
			// Cámara con una vlan que no está en gem_config: se separa.
			nombre: "camara con vlan fuera de gem_config -> vlan_sin_gem_config",
			o: OnuRepaso{Puerto: 20, OntID: 13, EstadoPresencia: "macs_multiples", Macs: []MacInfo{
				macReal("AA", 4000, false, false), macReal("BB", 3995, false, false)}},
			esperado:     "repaso_vlan_sin_gem_config",
			esperaNClien: 2,
			esperaMotivo: "vlans sin gem_config: 3995",
		},
		{
			// El matcheo ambiguo es un problema más básico y conserva la prioridad.
			nombre: "clientes ambiguos gana a vlan sin gem",
			o: OnuRepaso{Puerto: 20, OntID: 14, EstadoPresencia: "mac_verificada", Macs: []MacInfo{
				{Mac: "AA", Vlan: 1280, ClientesCtAmbiguos: true, ClientesCtTotal: 2, ClientesCtActivos: 2}}},
			esperado:     "repaso_clientes_ct_ambiguos",
			esperaNClien: 1,
		},
		{
			nombre:       "solo placeholder 00:00 -> caso 4",
			o:            OnuRepaso{Puerto: 2, OntID: 5, EstadoPresencia: "sin_trafico", PlaceholderIdx: []int{3, 7}},
			esperado:     "repaso_onu_sin_mac_placeholder",
			esperaNClien: 0,
		},
		{
			nombre:       "mixta real + placeholder",
			o:            OnuRepaso{Puerto: 2, OntID: 6, EstadoPresencia: "mac_verificada", Macs: []MacInfo{macReal("AA", 65, true, true)}, PlaceholderIdx: []int{4}},
			esperado:     "repaso_onu_mixta_real_y_placeholder",
			esperaNClien: 1,
		},
		{
			nombre:       "vlan 1001 aire",
			o:            OnuRepaso{Puerto: 3, OntID: 1, EstadoPresencia: "mac_verificada", Macs: []MacInfo{macReal("AA", 1001, false, false)}},
			esperado:     "repaso_vlan_1001_aire",
			esperaNClien: 1,
		},
		{
			nombre:       "vlan >3000 con cliente",
			o:            OnuRepaso{Puerto: 3, OntID: 2, EstadoPresencia: "macs_multiples", Macs: []MacInfo{macReal("AA", 3996, true, true)}},
			esperado:     "repaso_vlan_mayor_3000_con_cliente",
			esperaNClien: 1,
		},
		{
			// Caso real 8/6: la ONU no está en la OLT, pero su MAC vieja tiene
			// cliente activo en un CT configurado. El servicio existe -> se migra.
			nombre: "ausente_sin_mac CON cliente -> estado propio y listo",
			o: OnuRepaso{Puerto: 8, OntID: 6, EstadoPresencia: "ausente_sin_mac",
				Sn: ns("TPLGF58E4F10"), MacOnu: "1027F58E4F17", YaPrincipal: true,
				EstadoPrincipal: EstadoPrevio{Estado: EstadoNoCargable, Listo: false},
				Macs:            []MacInfo{macReal("1027F58E4F17", 50, true, true)}},
			esperado:     "sobrescrito_repaso_ausente_sin_mac_con_cliente",
			esperaNClien: 1,
			esperaListo:  true,
			esperaMotivo: "onu ausente en la olt pero con cliente; mac vieja: 1027F58E4F17",
		},
		{
			// El cliente en un CT no configurado también cuenta como cliente.
			nombre: "ausente_sin_mac con cliente en otro CT -> también listo",
			o: OnuRepaso{Puerto: 8, OntID: 7, EstadoPresencia: "ausente_sin_mac",
				Sn: ns("TPLGF58E4F10"), MacOnu: "1027F58E4F17",
				Macs: []MacInfo{{Mac: "1027F58E4F17", Vlan: 50, EnOtroCt: true, Pppoe: ns("cli/7")}}},
			esperado:     "repaso_ausente_sin_mac_con_cliente",
			esperaNClien: 1,
			esperaListo:  true,
		},
		{
			nombre:       "ausente_sin_mac patrón coincide",
			o:            OnuRepaso{Puerto: 4, OntID: 1, EstadoPresencia: "ausente_sin_mac", Sn: ns("TPLG328EA6F8"), MacOnu: "00:00:32:8E:A6:FF"},
			esperado:     "repaso_ausente_sin_mac_patron_ok_sin_cliente",
			esperaNClien: 0,
		},
		{
			nombre:       "ausente_sin_mac patrón NO coincide",
			o:            OnuRepaso{Puerto: 4, OntID: 2, EstadoPresencia: "ausente_sin_mac", Sn: ns("TPLG328EA6F8"), MacOnu: "00:00:11:22:33:44"},
			esperado:     "repaso_ausente_sin_mac_patron_no_coincide",
			esperaNClien: 0,
		},
		{
			nombre:       "mac sin onu en el puerto (caso 10)",
			o:            OnuRepaso{Puerto: 8, OntID: 1, SinOnu: true, Macs: []MacInfo{macReal("AA", 65, false, false)}},
			esperado:     "repaso_mac_sin_onu_en_puerto",
			esperaNClien: 1,
		},
		{
			nombre:     "ausente_sin_sn SIN macs -> ignorar",
			o:          OnuRepaso{Puerto: 4, OntID: 5, EstadoPresencia: "ausente_sin_sn"},
			esperaSkip: true,
		},
		{
			nombre:     "ausente_sin_sn CON macs -> igual se ignora (no existe la onu)",
			o:          OnuRepaso{Puerto: 4, OntID: 6, EstadoPresencia: "ausente_sin_sn", Macs: []MacInfo{macReal("AA", 65, false, false)}},
			esperaSkip: true,
		},
		{
			nombre: "varias macs con vlan camara (>3000) sin cliente -> camara (prioridad sobre multimac)",
			o: OnuRepaso{Puerto: 15, OntID: 3, EstadoPresencia: "macs_multiples", Macs: []MacInfo{
				macReal("AA", 4000, false, false), macReal("BB", 3996, false, false)}},
			esperado:     "repaso_vlan_mayor_3000_camara",
			esperaNClien: 2,
			esperaListo:  true,
		},
		{
			// Forma real de producción: 2 clientes en CT, exactamente 1 activo, y
			// el activo es el que quedó en el pppoe. El matcheo es confiable -> listo.
			nombre: "varios clientes en CT, 1 activo con pppoe -> listo",
			o: OnuRepaso{Puerto: 2, OntID: 7, EstadoPresencia: "mac_verificada", Macs: []MacInfo{
				{Mac: "AA", Vlan: 65, TieneCliente: true, Activo: true, VariosClientesCt: true,
					ClientesCtTotal: 2, ClientesCtActivos: 1, Pppoe: ns("cli/1")}}},
			esperado:     "repaso_listo",
			esperaNClien: 1,
			esperaListo:  true,
			esperaMotivo: "resuelto entre 2 clientes en CT (1 activo): cli/1",
		},
		{
			// Mismo caso pero ya cargado por la 1ª pasada: se re-emite el estado
			// previo (principal_listo), que también es listo.
			nombre: "varios clientes en CT resuelto y ya cargado -> conserva principal_listo",
			o: OnuRepaso{Puerto: 2, OntID: 8, EstadoPresencia: "mac_verificada", YaPrincipal: true,
				EstadoPrincipal: EstadoPrevio{Estado: EstadoListo, Listo: true},
				Macs: []MacInfo{
					{Mac: "AA", Vlan: 65, TieneCliente: true, Activo: true, VariosClientesCt: true,
						ClientesCtTotal: 2, ClientesCtActivos: 1, Pppoe: ns("cli/1")}}},
			esperado:     EstadoListo,
			esperaNClien: 1,
			esperaListo:  true,
			esperaMotivo: "resuelto entre 2 clientes en CT (1 activo)",
		},
		{
			// El activo se eligió pero quedó sin pppoe: no hay qué provisionar,
			// así que NO se promueve.
			nombre: "varios clientes en CT, 1 activo SIN pppoe -> no se promueve",
			o: OnuRepaso{Puerto: 2, OntID: 9, EstadoPresencia: "mac_verificada", Macs: []MacInfo{
				{Mac: "AA", Vlan: 65, TieneCliente: true, Activo: true, VariosClientesCt: true,
					ClientesCtTotal: 2, ClientesCtActivos: 1}}},
			esperado:     "repaso_varios_clientes_en_ct",
			esperaNClien: 1,
		},
		{
			// Defensivo: si el elegido no es el activo, tampoco se promueve.
			nombre: "varios clientes en CT, elegido NO activo -> no se promueve",
			o: OnuRepaso{Puerto: 2, OntID: 11, EstadoPresencia: "mac_verificada", Macs: []MacInfo{
				{Mac: "AA", Vlan: 65, TieneCliente: true, Activo: false, VariosClientesCt: true,
					ClientesCtTotal: 2, ClientesCtActivos: 1, Pppoe: ns("cli/1")}}},
			esperado:     "repaso_varios_clientes_en_ct",
			esperaNClien: 1,
		},
		{
			// 1 MAC con cliente identificado en un CT fuera de la lista: se migra
			// con los datos de ese controlador.
			nombre: "1 mac sin cliente configurado pero en otro CT -> listo con esos datos",
			o: OnuRepaso{Puerto: 1, OntID: 9, EstadoPresencia: "mac_verificada", Macs: []MacInfo{
				{Mac: "AA", Vlan: 65, EnOtroCt: true, Pppoe: ns("cli/9"),
					Plan: ns("100 MB Hogar"), IpControlador: ns("172.16.4.29")}}},
			esperado:     "repaso_encontrado_en_otro_ct",
			esperaNClien: 1,
			esperaListo:  true,
			esperaMotivo: "datos tomados del ct 172.16.4.29 (fuera de los configurados)",
		},
		{
			// Varias MACs: la ambigüedad es cuál corresponde al cliente, así que
			// tiene estado propio y NO va listo.
			nombre: "multimac sin cliente config, alguna en otro CT -> multimac_en_otro_ct, no listo",
			o: OnuRepaso{Puerto: 1, OntID: 10, EstadoPresencia: "macs_multiples", Macs: []MacInfo{
				{Mac: "AA", Vlan: 65}, {Mac: "BB", Vlan: 65, EnOtroCt: true, Pppoe: ns("cli/10")}}},
			esperado:     "repaso_onu_multimac_en_otro_ct",
			esperaNClien: 2,
		},
		{
			nombre: "camara con una mac con cliente -> con_cliente",
			o: OnuRepaso{Puerto: 15, OntID: 4, EstadoPresencia: "macs_multiples", Macs: []MacInfo{
				macReal("AA", 3991, true, true), macReal("BB", 4000, false, false)}},
			esperado:     "repaso_vlan_mayor_3000_con_cliente",
			esperaNClien: 2,
		},
		{
			nombre: "varios clientes en CT con 2 activos -> ambiguo, sin cliente",
			o: OnuRepaso{Puerto: 5, OntID: 1, EstadoPresencia: "mac_verificada", Macs: []MacInfo{
				{Mac: "AA", Vlan: 65, ClientesCtAmbiguos: true, ClientesCtTotal: 3, ClientesCtActivos: 2}}},
			esperado:     "repaso_clientes_ct_ambiguos",
			esperaNClien: 1,
			esperaMotivo: "AA (3 clientes, 2 activos)",
		},
		{
			nombre: "varios clientes en CT con 0 activos -> ambiguo",
			o: OnuRepaso{Puerto: 5, OntID: 2, EstadoPresencia: "mac_verificada", Macs: []MacInfo{
				{Mac: "AA", Vlan: 65, ClientesCtAmbiguos: true, ClientesCtTotal: 2, ClientesCtActivos: 0}}},
			esperado:     "repaso_clientes_ct_ambiguos",
			esperaNClien: 1,
			esperaMotivo: "0 activos",
		},
		{
			nombre: "ambiguo tiene prioridad sobre camara y multimac",
			o: OnuRepaso{Puerto: 5, OntID: 3, EstadoPresencia: "macs_multiples", Macs: []MacInfo{
				macReal("AA", 4000, true, true),
				{Mac: "BB", Vlan: 65, ClientesCtAmbiguos: true, ClientesCtTotal: 2, ClientesCtActivos: 2}}},
			esperado:     "repaso_clientes_ct_ambiguos",
			esperaNClien: 2,
		},
		{
			// Una MAC con varias vlans es UN equipo con varios servicios: no debe
			// caer en las ramas multi-mac, que son para varios equipos.
			nombre: "1 mac con 3 vlans -> multiservicio, no multimac",
			o: OnuRepaso{Puerto: 20, OntID: 1, EstadoPresencia: "macs_multiples", Macs: []MacInfo{
				macReal("AA", 65, true, true), macReal("AA", 1001, false, false),
				macReal("AA", 102, false, false)}},
			esperado:     "repaso_onu_mac_multiservicio",
			esperaNClien: 3,
			esperaMotivo: "vlans: 65,102,1001",
		},
		{
			nombre: "2 macs distintas con cliente -> sigue siendo dos_macs",
			o: OnuRepaso{Puerto: 20, OntID: 2, EstadoPresencia: "macs_multiples", Macs: []MacInfo{
				macReal("AA", 65, true, true), macReal("BB", 65, true, true)}},
			esperado:     "repaso_onu_dos_macs_dos_clientes",
			esperaNClien: 2,
			esperaListo:  true,
		},
		{
			// 2 equipos aunque haya 3 filas: la MAC AA presta dos servicios.
			nombre: "2 macs, una con 2 vlans -> dos_macs (se cuentan equipos)",
			o: OnuRepaso{Puerto: 20, OntID: 3, EstadoPresencia: "macs_multiples", Macs: []MacInfo{
				macReal("AA", 65, true, true), macReal("AA", 102, true, true),
				macReal("BB", 65, true, true)}},
			esperado:     "repaso_onu_dos_macs_dos_clientes",
			esperaNClien: 3,
			esperaListo:  true,
			esperaMotivo: "vlans: 65,102",
		},
		{
			// Cámara mantiene la prioridad, pero el motivo deja ver el multiservicio.
			nombre: "1 mac con internet + camara -> camara, con las vlans en el motivo",
			o: OnuRepaso{Puerto: 20, OntID: 4, EstadoPresencia: "macs_multiples", Macs: []MacInfo{
				macReal("AA", 65, true, true), macReal("AA", 3996, false, false)}},
			esperado:     "repaso_vlan_mayor_3000_con_cliente",
			esperaNClien: 2,
			esperaMotivo: "vlans: 65,3996",
		},
		{
			// El detalle de los 00:00 se agrega sea cual sea el caso, no sólo en
			// los casos 4: es la referencia visual de los índices vacíos.
			nombre: "el motivo de placeholders se agrega tambien en otros casos",
			o: OnuRepaso{Puerto: 3, OntID: 9, EstadoPresencia: "mac_verificada", PlaceholderIdx: []int{5, 2},
				Macs: []MacInfo{macReal("AA", 1001, false, false)}},
			esperado:     "repaso_vlan_1001_aire",
			esperaNClien: 1,
			esperaMotivo: "indices_00: 2,5",
		},
	}

	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			onu, cli, skip := ClasificarRepaso(c.o, 4, planes, gem)
			if skip != c.esperaSkip {
				t.Fatalf("skip=%v, esperaba %v", skip, c.esperaSkip)
			}
			if skip {
				return
			}
			if onu.Estado != c.esperado {
				t.Fatalf("estado onu=%q, esperaba %q", onu.Estado, c.esperado)
			}
			if len(cli) != c.esperaNClien {
				t.Fatalf("nº clientes=%d, esperaba %d", len(cli), c.esperaNClien)
			}
			if onu.Listo != c.esperaListo {
				t.Fatalf("onu listo=%v, esperaba %v", onu.Listo, c.esperaListo)
			}
			if c.esperaMotivo != "" && !strings.Contains(onu.Motivo, c.esperaMotivo) {
				t.Fatalf("motivo=%q, esperaba que contuviera %q", onu.Motivo, c.esperaMotivo)
			}
			for _, x := range cli {
				if x.Estado != c.esperado {
					t.Fatalf("cliente estado=%q, esperaba %q (cascada)", x.Estado, c.esperado)
				}
				if x.Listo != c.esperaListo {
					t.Fatalf("cliente listo=%v, esperaba %v (cascada)", x.Listo, c.esperaListo)
				}
			}
		})
	}
}

// Una MAC ambigua no debe llegar con cliente asignado, y la fila cliente tiene
// que explicar por qué en su propio motivo.
func TestClasificarRepasoAmbiguoSinCliente(t *testing.T) {
	o := OnuRepaso{Puerto: 5, OntID: 1, EstadoPresencia: "mac_verificada", Macs: []MacInfo{
		{Mac: "AA", Vlan: 65, ClientesCtAmbiguos: true, ClientesCtTotal: 3, ClientesCtActivos: 2}}}
	_, cli, _ := ClasificarRepaso(o, 4, nil, nil)
	if cli[0].Pppoe != "" {
		t.Fatalf("pppoe=%q, una mac ambigua no debe traer cliente", cli[0].Pppoe)
	}
	if !strings.Contains(cli[0].Motivo, "3 clientes en CT, 2 activos") {
		t.Fatalf("motivo del cliente=%q, esperaba el conteo de clientes en CT", cli[0].Motivo)
	}
}

// TestResolverMacs cubre el paso que antes vivía suelto en cmd/repaso: pasar de
// las filas crudas de GetMacsHistoricasRepaso (una por match mac × cliente) a
// una MacInfo por servicio.
func TestResolverMacs(t *testing.T) {
	const cfg1, cfg2, ajeno = "172.16.4.5", "172.16.4.14", "172.16.4.29"
	configurados := []string{cfg1, cfg2}

	// fila arma un match crudo tal como lo devuelve la consulta.
	fila := func(mac string, vlan int, ipCT, pppoe string, activo int64, fechaCt string) MacRepaso {
		return MacRepaso{
			Mac: mac, Puerto: 6, OntID: 18, Vlan: vlan, Idx: ni(1),
			FechaEq: ns("2026-07-29 12:42:30"),
			Pppoe:   ns(pppoe), Activo: sql.NullInt64{Int64: activo, Valid: true},
			FechaCt: ns(fechaCt), Plan: ns("100 MB Hogar"), NroCliente: ni(4321),
			IpCliente: ns("10.0.0.9"), IpControlador: ns(ipCT),
		}
	}
	unica := func(m map[[2]int][]MacInfo) MacInfo {
		t.Helper()
		macs := m[[2]int{6, 18}]
		if len(macs) != 1 {
			t.Fatalf("se resolvieron %d servicios, esperaba 1: %+v", len(macs), macs)
		}
		return macs[0]
	}

	t.Run("cliente SOLO en un CT no configurado: se copian todos los datos", func(t *testing.T) {
		mi := unica(ResolverMacs([]MacRepaso{
			fila("AA", 600, ajeno, "cli/9", 1, "2026-07-30 10:00:00"),
		}, configurados))

		if !mi.EnOtroCt {
			t.Fatalf("EnOtroCt=false, esperaba true")
		}
		if mi.TieneCliente {
			t.Errorf("TieneCliente=true: el CT no está en la lista de configurados")
		}
		if nn(mi.Pppoe) != "cli/9" {
			t.Errorf("pppoe=%q, esperaba cli/9 — es el dato que se estaba perdiendo", nn(mi.Pppoe))
		}
		if nn(mi.Plan) != "100 MB Hogar" || mi.NroCliente.Int64 != 4321 {
			t.Errorf("plan=%q nro=%+v, esperaba los datos del otro ct", nn(mi.Plan), mi.NroCliente)
		}
		if nn(mi.IpCliente) != "10.0.0.9" || nn(mi.IpControlador) != ajeno {
			t.Errorf("ips=%q/%q, esperaba las del otro ct", nn(mi.IpCliente), nn(mi.IpControlador))
		}
	})

	t.Run("si tambien esta en un CT configurado, gana el configurado", func(t *testing.T) {
		mi := unica(ResolverMacs([]MacRepaso{
			fila("AA", 600, ajeno, "cli/ajeno", 1, "2026-07-30 10:00:00"),
			fila("AA", 600, cfg1, "cli/propio", 1, "2026-07-30 10:00:00"),
		}, configurados))

		if mi.EnOtroCt || !mi.TieneCliente {
			t.Fatalf("EnOtroCt=%v TieneCliente=%v, esperaba false/true", mi.EnOtroCt, mi.TieneCliente)
		}
		if nn(mi.Pppoe) != "cli/propio" {
			t.Errorf("pppoe=%q, esperaba el del CT configurado", nn(mi.Pppoe))
		}
	})

	t.Run("varios en CT configurados: gana el activo", func(t *testing.T) {
		mi := unica(ResolverMacs([]MacRepaso{
			fila("AA", 600, cfg1, "cli/viejo", 0, "2026-07-31 10:00:00"), // más nuevo pero inactivo
			fila("AA", 600, cfg2, "cli/activo", 1, "2026-01-01 10:00:00"),
		}, configurados))

		if nn(mi.Pppoe) != "cli/activo" {
			t.Errorf("pppoe=%q, el activo debe ganarle al más reciente", nn(mi.Pppoe))
		}
		if !mi.VariosClientesCt || mi.ClientesCtTotal != 2 || mi.ClientesCtActivos != 1 {
			t.Errorf("varios=%v total=%d activos=%d, esperaba true/2/1",
				mi.VariosClientesCt, mi.ClientesCtTotal, mi.ClientesCtActivos)
		}
	})

	t.Run("varios en CT configurados sin un unico activo: sin cliente", func(t *testing.T) {
		mi := unica(ResolverMacs([]MacRepaso{
			fila("AA", 600, cfg1, "cli/a", 1, "2026-07-30 10:00:00"),
			fila("AA", 600, cfg2, "cli/b", 1, "2026-07-31 10:00:00"),
		}, configurados))

		if !mi.ClientesCtAmbiguos {
			t.Fatalf("ClientesCtAmbiguos=false, esperaba true con 2 activos")
		}
		if nn(mi.Pppoe) != "" || mi.TieneCliente {
			t.Errorf("pppoe=%q TieneCliente=%v, no debe asignarse cliente", nn(mi.Pppoe), mi.TieneCliente)
		}
	})

	t.Run("una MAC con dos vlans se resuelve como dos servicios", func(t *testing.T) {
		macs := ResolverMacs([]MacRepaso{
			fila("AA", 600, ajeno, "cli/9", 1, "2026-07-30 10:00:00"),
			fila("AA", 65, cfg1, "cli/10", 1, "2026-07-30 10:00:00"),
		}, configurados)[[2]int{6, 18}]

		if len(macs) != 2 {
			t.Fatalf("se resolvieron %d servicios, esperaba 2 (una vlan cada uno)", len(macs))
		}
	})

	t.Run("sin match en CT no se inventa cliente", func(t *testing.T) {
		mi := unica(ResolverMacs([]MacRepaso{{
			Mac: "AA", Puerto: 6, OntID: 18, Vlan: 600, Idx: ni(1),
			FechaEq: ns("2026-07-29 12:42:30"), // Pppoe inválido: LEFT JOIN sin match
		}}, configurados))

		if mi.TieneCliente || mi.EnOtroCt || nn(mi.Pppoe) != "" {
			t.Errorf("mi=%+v, esperaba una MAC huérfana", mi)
		}
	})
}

// Una MAC hallada en un CT fuera de los configurados tiene que llegar a
// registro_cliente con TODOS los datos de ese controlador, y con plan_id
// resuelto igual que cualquier otro cliente.
func TestClasificarRepasoOtroCtCopiaDatos(t *testing.T) {
	planes := map[string]int{"100 MB Hogar": 1}
	gem := map[int]int{65: 7}

	o := OnuRepaso{Puerto: 1, OntID: 9, EstadoPresencia: "mac_verificada", Macs: []MacInfo{{
		Mac: "AA", Vlan: 65, EnOtroCt: true,
		Pppoe: ns("cli/9"), Plan: ns("100 MB Hogar"), NroCliente: ni(4321),
		IpCliente: ns("10.0.0.9"), IpControlador: ns("172.16.4.29"),
	}}}
	onu, cli, _ := ClasificarRepaso(o, 4, planes, gem)

	if !onu.Listo {
		t.Fatalf("onu.Listo=false, esperaba listo para migrar")
	}
	c := cli[0]
	if c.Pppoe != "cli/9" {
		t.Errorf("pppoe=%q, esperaba cli/9", c.Pppoe)
	}
	if c.Plan != "100 MB Hogar" || !c.PlanID.Valid || c.PlanID.Int64 != 1 {
		t.Errorf("plan=%q plan_id=%+v, esperaba el plan resuelto", c.Plan, c.PlanID)
	}
	if !c.NroCliente.Valid || c.NroCliente.Int64 != 4321 {
		t.Errorf("nro_cliente=%+v, esperaba 4321", c.NroCliente)
	}
	if nn(c.IpCliente) != "10.0.0.9" || nn(c.IpControlador) != "172.16.4.29" {
		t.Errorf("ips=%q/%q, esperaba las del otro ct", nn(c.IpCliente), nn(c.IpControlador))
	}
	if !c.Listo {
		t.Errorf("cliente listo=false, la cascada debe marcarlo igual que la ONU")
	}
	if !strings.Contains(c.Motivo, "172.16.4.29") {
		t.Errorf("motivo del cliente=%q, esperaba que nombrara el ct de origen", c.Motivo)
	}
}

// Si el otro CT no trae plan ni nro de cliente, esos campos quedan vacíos pero
// la fila se emite igual.
func TestClasificarRepasoOtroCtSinPlan(t *testing.T) {
	o := OnuRepaso{Puerto: 1, OntID: 9, EstadoPresencia: "mac_verificada", Macs: []MacInfo{{
		Mac: "AA", Vlan: 65, EnOtroCt: true,
		Pppoe: ns("cli/9"), IpControlador: ns("172.16.4.29"),
	}}}
	// gem tiene la vlan 65: acá se prueba que falte el PLAN, no el gem.
	onu, cli, _ := ClasificarRepaso(o, 4, map[string]int{}, map[int]int{65: 1})

	if !onu.Listo {
		t.Fatalf("onu.Listo=false, esperaba listo aunque falte el plan")
	}
	c := cli[0]
	if c.Plan != "" || c.PlanID.Valid {
		t.Errorf("plan=%q plan_id=%+v, esperaba vacío", c.Plan, c.PlanID)
	}
	if c.NroCliente.Valid {
		t.Errorf("nro_cliente=%+v, esperaba vacío", c.NroCliente)
	}
	if c.Pppoe != "cli/9" {
		t.Errorf("pppoe=%q, el que sí vino debe quedar", c.Pppoe)
	}
}

func TestNormalizarSn(t *testing.T) {
	casos := []struct{ entrada, esperado string }{
		{"TPLGF58E9CB8", "TPLGF58E9CB8"},
		{"TPLG-F58E9CB8", "TPLGF58E9CB8"}, // la misma con guión
		{"  tplg-f58e9cb8  ", "TPLGF58E9CB8"},
		{"TPLG:F58E9CB8", "TPLGF58E9CB8"},
		{"", ""},
		{"---", ""},
	}
	for _, c := range casos {
		if got := NormalizarSn(c.entrada); got != c.esperado {
			t.Errorf("NormalizarSn(%q) = %q, esperaba %q", c.entrada, got, c.esperado)
		}
	}
}

func TestIndexarVerificadasPorSn(t *testing.T) {
	// El mismo equipo escrito con dos convenciones distintas, en dos OLTs
	// distintas, tiene que caer bajo la MISMA clave.
	idx := IndexarVerificadasPorSn([]OnuVerificada{
		{IDOlt: 4, Puerto: 5, OntID: 12, Sn: "TPLG-F58E9CB8"},
		{IDOlt: 7, Puerto: 2, OntID: 15, Sn: "TPLGF58E9CB8"},
		{IDOlt: 9, Puerto: 1, OntID: 1, Sn: "HWTC1234ABCD"},
		{IDOlt: 9, Puerto: 1, OntID: 2, Sn: "---"}, // no normaliza a nada: se descarta
	})
	if len(idx) != 2 {
		t.Fatalf("idx tiene %d claves, esperaba 2: %v", len(idx), idx)
	}
	if got := len(idx["TPLGF58E9CB8"]); got != 2 {
		t.Fatalf("la clave normalizada agrupó %d, esperaba 2 (con y sin guión)", got)
	}
}

func TestResolverDuplicadoSn(t *testing.T) {
	// El SN está verificado en tres lugares: dos posiciones de la olt 4 (una es
	// la propia) y una de la olt 7.
	refs := []OnuVerificada{
		{IDOlt: 4, Puerto: 5, OntID: 40, Sn: "TPLGF58E9CB8"},  // la propia posición
		{IDOlt: 4, Puerto: 5, OntID: 12, Sn: "TPLG-F58E9CB8"}, // otra de la misma olt
		{IDOlt: 7, Puerto: 2, OntID: 15, Sn: "TPLG-F58E9CB8"}, // otra olt
	}

	t.Run("separa misma olt de otra olt y excluye la propia posicion", func(t *testing.T) {
		misma, otra := ResolverDuplicadoSn(refs, 4, 5, 40)
		if misma == nil || misma.Puerto != 5 || misma.OntID != 12 {
			t.Fatalf("misma=%+v, esperaba 5/12", misma)
		}
		if otra == nil || otra.IDOlt != 7 {
			t.Fatalf("otra=%+v, esperaba la de la olt 7", otra)
		}
	})

	t.Run("sin duplicados devuelve nil, nil", func(t *testing.T) {
		// Desde la posición 5/12 y con sólo ella misma en el índice.
		misma, otra := ResolverDuplicadoSn(
			[]OnuVerificada{{IDOlt: 4, Puerto: 5, OntID: 12, Sn: "X"}}, 4, 5, 12)
		if misma != nil || otra != nil {
			t.Fatalf("misma=%+v otra=%+v, esperaba nil/nil", misma, otra)
		}
	})

	t.Run("el resultado no depende del orden de entrada", func(t *testing.T) {
		// Dos candidatos en otras olts: siempre debe ganar el menor (olt 5).
		a := OnuVerificada{IDOlt: 9, Puerto: 1, OntID: 1, Sn: "X"}
		b := OnuVerificada{IDOlt: 5, Puerto: 8, OntID: 3, Sn: "X"}
		for _, entrada := range [][]OnuVerificada{{a, b}, {b, a}} {
			_, otra := ResolverDuplicadoSn(entrada, 4, 1, 1)
			if otra == nil || otra.IDOlt != 5 {
				t.Fatalf("otra=%+v, esperaba la olt 5 (la menor)", otra)
			}
		}
	})
}

func TestDedupMacs(t *testing.T) {
	// srv arma un servicio (mac, vlan) con su idx y su fecha de última aparición.
	srv := func(mac string, vlan int, idx int64, fecha string) MacInfo {
		return MacInfo{Mac: mac, Vlan: vlan, Idx: ni(idx), FechaEq: ns(fecha)}
	}
	sinIdx := func(mac string, vlan int) MacInfo { return MacInfo{Mac: mac, Vlan: vlan} }

	viejo, nuevo := "2026-01-01 10:00:00", "2026-07-30 10:00:00"

	t.Run("misma mac y vlan, mismo idx: queda la ultima", func(t *testing.T) {
		cons, desc := DedupMacs([]MacInfo{
			srv("AA", 65, 3, viejo),
			srv("AA", 65, 3, nuevo),
		})
		if len(cons) != 1 || nn(cons[0].FechaEq) != nuevo {
			t.Fatalf("conservadas=%v, esperaba sólo la más reciente", cons)
		}
		if len(desc) != 1 {
			t.Fatalf("descartadas=%d, esperaba 1", len(desc))
		}
	})

	t.Run("misma mac y vlan, distinto idx: queda la ultima", func(t *testing.T) {
		cons, _ := DedupMacs([]MacInfo{
			srv("AA", 65, 3, viejo),
			srv("AA", 65, 9, nuevo),
		})
		if len(cons) != 1 || cons[0].Idx.Int64 != 9 {
			t.Fatalf("conservadas=%v, esperaba sólo el idx 9 (reasignado)", cons)
		}
	})

	t.Run("misma mac, distinta vlan y distinto idx: conviven", func(t *testing.T) {
		cons, desc := DedupMacs([]MacInfo{
			srv("AA", 65, 3, viejo),
			srv("AA", 3996, 9, nuevo),
		})
		if len(cons) != 2 || len(desc) != 0 {
			t.Fatalf("conservadas=%d descartadas=%d, esperaba 2 y 0 (dos servicios)", len(cons), len(desc))
		}
	})

	t.Run("misma mac, distinta vlan, MISMO idx: queda la ultima", func(t *testing.T) {
		cons, _ := DedupMacs([]MacInfo{
			srv("AA", 65, 3, viejo),
			srv("AA", 3996, 3, nuevo),
		})
		if len(cons) != 1 || cons[0].Vlan != 3996 {
			t.Fatalf("conservadas=%v, esperaba sólo la más reciente (un idx = una boca)", cons)
		}
	})

	t.Run("macs distintas con el mismo idx: queda la ultima", func(t *testing.T) {
		cons, desc := DedupMacs([]MacInfo{
			srv("VIEJA", 65, 3, viejo),
			srv("NUEVA", 65, 3, nuevo),
		})
		if len(cons) != 1 || cons[0].Mac != "NUEVA" {
			t.Fatalf("conservadas=%v, esperaba sólo NUEVA", cons)
		}
		if len(desc) != 1 || desc[0].Mac != "VIEJA" {
			t.Fatalf("descartadas=%v, esperaba VIEJA", desc)
		}
	})

	t.Run("macs distintas con el mismo idx, orden inverso: igual queda la ultima", func(t *testing.T) {
		cons, _ := DedupMacs([]MacInfo{
			srv("NUEVA", 65, 3, nuevo),
			srv("VIEJA", 65, 3, viejo),
		})
		if len(cons) != 1 || cons[0].Mac != "NUEVA" {
			t.Fatalf("conservadas=%v, esperaba sólo NUEVA", cons)
		}
	})

	t.Run("macs e idx distintos: conviven", func(t *testing.T) {
		cons, desc := DedupMacs([]MacInfo{
			srv("AA", 65, 1, viejo),
			srv("BB", 65, 2, nuevo),
		})
		if len(cons) != 2 || len(desc) != 0 {
			t.Fatalf("conservadas=%d descartadas=%d, esperaba 2 y 0", len(cons), len(desc))
		}
	})

	t.Run("idx NULL: sólo se colapsa por (mac, vlan)", func(t *testing.T) {
		cons, desc := DedupMacs([]MacInfo{sinIdx("AA", 65), sinIdx("BB", 65), sinIdx("AA", 1001)})
		if len(cons) != 3 || len(desc) != 0 {
			t.Fatalf("conservadas=%d descartadas=%d, esperaba 3 y 0", len(cons), len(desc))
		}
		cons, _ = DedupMacs([]MacInfo{sinIdx("AA", 65), sinIdx("AA", 65)})
		if len(cons) != 1 {
			t.Fatalf("conservadas=%d, esperaba 1 (mismo servicio)", len(cons))
		}
	})

	t.Run("empate de fecha: gana la primera, estable", func(t *testing.T) {
		cons, _ := DedupMacs([]MacInfo{
			srv("PRIMERA", 65, 3, nuevo),
			srv("SEGUNDA", 65, 3, nuevo),
		})
		if len(cons) != 1 || cons[0].Mac != "PRIMERA" {
			t.Fatalf("conservadas=%v, esperaba PRIMERA", cons)
		}
	})
}

func TestPlaceholdersLibres(t *testing.T) {
	macs := []MacInfo{{Mac: "AA", Idx: ni(3)}, {Mac: "BB"}} // BB sin idx: no ocupa nada

	libres := PlaceholdersLibres([]int{3, 5, 7}, macs)
	if len(libres) != 2 || libres[0] != 5 || libres[1] != 7 {
		t.Fatalf("libres=%v, esperaba [5 7] (el 3 lo ocupa una mac real)", libres)
	}
	if l := PlaceholdersLibres([]int{3}, macs); len(l) != 0 {
		t.Fatalf("libres=%v, esperaba vacío", l)
	}
	if l := PlaceholdersLibres(nil, macs); len(l) != 0 {
		t.Fatalf("libres=%v, esperaba vacío", l)
	}
}

var _ = sql.NullString{}
