package database

import (
	"database/sql"
	"testing"
)

func ns(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }
func ni(n int64) sql.NullInt64   { return sql.NullInt64{Int64: n, Valid: true} }

// fila base "feliz": 1 mac, cliente activo, plan y sn ok, vlan residencial 65.
func filaOK(puerto, ont int, mac, pppoe string, vlan int) FilaCarga {
	return FilaCarga{
		MacWan: mac, Pppoe: pppoe, Vlan: vlan, PuertoPon: puerto, OnuID: ont,
		Indice: ni(1), EsActivo: 1, Plan: ns("100 MB Hogar"), Sn: ns("TPLG12345678"),
		OnuIdentificador: ni(int64(ont)), EstadoPresencia: ns("mac_verificada"),
		CantidadMacs: ni(1),
	}
}

func TestClasificarPrimeraPasada(t *testing.T) {
	planes := map[string]int{"100 MB Hogar": 1}
	gem := map[int]int{65: 1, 20: 1}

	t.Run("caso feliz -> listo", func(t *testing.T) {
		onus, cli := ClasificarPrimeraPasada([]FilaCarga{filaOK(9, 9, "AA", "cli/1", 65)}, 4, planes, gem)
		if len(onus) != 1 || len(cli) != 1 {
			t.Fatalf("esperaba 1 onu y 1 cliente, got %d/%d", len(onus), len(cli))
		}
		if onus[0].Estado != EstadoListo || !onus[0].Listo {
			t.Fatalf("onu estado=%s listo=%v", onus[0].Estado, onus[0].Listo)
		}
		if !cli[0].Listo || cli[0].Estado != EstadoListo {
			t.Fatalf("cliente estado=%s listo=%v", cli[0].Estado, cli[0].Listo)
		}
		if !cli[0].GemID.Valid || cli[0].GemID.Int64 != 1 {
			t.Fatalf("gem_id esperado 1, got %+v", cli[0].GemID)
		}
		if !cli[0].PlanID.Valid || cli[0].PlanID.Int64 != 1 {
			t.Fatalf("plan_id esperado 1, got %+v", cli[0].PlanID)
		}
	})

	t.Run("dos macs misma onu -> caso 1 en onu y clientes", func(t *testing.T) {
		f1 := filaOK(6, 40, "AA", "cli/1", 65)
		f2 := filaOK(6, 40, "BB", "cli/2", 65)
		onus, cli := ClasificarPrimeraPasada([]FilaCarga{f1, f2}, 4, planes, gem)
		if len(onus) != 1 || len(cli) != 2 {
			t.Fatalf("esperaba 1 onu y 2 clientes, got %d/%d", len(onus), len(cli))
		}
		if onus[0].Estado != EstadoOnuDosMacs || onus[0].Listo {
			t.Fatalf("onu estado=%s listo=%v", onus[0].Estado, onus[0].Listo)
		}
		for _, c := range cli {
			if c.Estado != EstadoOnuDosMacs || c.Listo {
				t.Fatalf("cliente %s estado=%s listo=%v", c.MacWan, c.Estado, c.Listo)
			}
		}
	})

	t.Run("vlan 1001 -> aire", func(t *testing.T) {
		_, cli := ClasificarPrimeraPasada([]FilaCarga{filaOK(1, 1, "AA", "cli/1", 1001)}, 4, planes, gem)
		if cli[0].Estado != EstadoVlan1001Aire || cli[0].Listo {
			t.Fatalf("estado=%s listo=%v", cli[0].Estado, cli[0].Listo)
		}
	})

	t.Run("vlan >3000 -> camara", func(t *testing.T) {
		_, cli := ClasificarPrimeraPasada([]FilaCarga{filaOK(1, 1, "AA", "cli/1", 3996)}, 4, planes, gem)
		if cli[0].Estado != EstadoVlanMayor3000 || cli[0].Listo {
			t.Fatalf("estado=%s listo=%v", cli[0].Estado, cli[0].Listo)
		}
	})

	t.Run("camara multi-mac -> onu y clientes en camara (prioridad)", func(t *testing.T) {
		f1 := filaOK(15, 3, "AA", "cli/1", 4000)
		f2 := filaOK(15, 3, "BB", "cli/2", 3996)
		onus, cli := ClasificarPrimeraPasada([]FilaCarga{f1, f2}, 4, planes, gem)
		if onus[0].Estado != EstadoVlanMayor3000 || onus[0].Listo {
			t.Fatalf("onu estado=%s listo=%v", onus[0].Estado, onus[0].Listo)
		}
		for _, c := range cli {
			if c.Estado != EstadoVlanMayor3000 {
				t.Fatalf("cliente %s estado=%s (esperaba cámara en ambas)", c.MacWan, c.Estado)
			}
		}
	})

	t.Run("sin onu (sn null) -> mac_sin_onu", func(t *testing.T) {
		f := filaOK(3, 1, "AA", "cli/1", 65)
		f.Sn = sql.NullString{}
		f.OnuIdentificador = sql.NullInt64{}
		_, cli := ClasificarPrimeraPasada([]FilaCarga{f}, 4, planes, gem)
		if cli[0].Estado != EstadoMacSinOnu || cli[0].Listo {
			t.Fatalf("estado=%s listo=%v", cli[0].Estado, cli[0].Listo)
		}
	})

	// El plan sin reconocer NO frena la carga: el cliente existe y el service-port
	// se crea igual; el plan se carga a mano en la OLT después.
	// INVARIANTE: registro_onu y registro_cliente nunca pueden discrepar. Si una
	// posición queda listo=1, TODAS sus filas de cliente tienen que estar en 1, y
	// con el mismo estado. Se prueba sobre un lote que mezcla todos los caminos de
	// clasificarFila, no caso por caso, para que un caso nuevo no se escape.
	t.Run("invariante: cliente y onu comparten estado y listo", func(t *testing.T) {
		feliz := filaOK(1, 1, "AA", "cli/1", 65)
		sinPlan := filaOK(1, 2, "BB", "cli/2", 65)
		sinPlan.Plan = ns("ONG 30 M") // plan no reconocido
		noCargable := filaOK(1, 3, "CC", "cli/3", 65)
		noCargable.EstadoPresencia = ns("sin_trafico")
		aire := filaOK(1, 4, "DD", "cli/4", 1001)
		camara := filaOK(1, 5, "EE", "cli/5", 3996)
		dosA := filaOK(1, 6, "FF", "cli/6", 65)
		dosB := filaOK(1, 6, "GG", "cli/7", 65) // misma posición: multi-MAC

		onus, cli := ClasificarPrimeraPasada(
			[]FilaCarga{feliz, sinPlan, noCargable, aire, camara, dosA, dosB}, 4, planes, gem)

		porPos := map[[2]int]RegistroOnu{}
		for _, o := range onus {
			porPos[[2]int{o.Puerto, o.OntID}] = o
		}
		if len(porPos) != 6 {
			t.Fatalf("se emitieron %d posiciones, esperaba 6", len(porPos))
		}
		for _, c := range cli {
			o, ok := porPos[[2]int{c.Puerto, c.OntID}]
			if !ok {
				t.Fatalf("cliente %s en %d/%d sin su fila de ONU", c.MacWan, c.Puerto, c.OntID)
			}
			if c.Estado != o.Estado {
				t.Errorf("%d/%d %s: estado cliente=%q onu=%q", c.Puerto, c.OntID, c.MacWan, c.Estado, o.Estado)
			}
			if c.Listo != o.Listo {
				t.Errorf("%d/%d %s: listo cliente=%v onu=%v", c.Puerto, c.OntID, c.MacWan, c.Listo, o.Listo)
			}
		}
	})

	t.Run("plan no reconocido -> se marca, pero va listo", func(t *testing.T) {
		f := filaOK(5, 1, "AA", "cli/1", 65)
		f.Plan = ns("ONG 30 M")
		_, cli := ClasificarPrimeraPasada([]FilaCarga{f}, 4, planes, gem)
		if cli[0].Estado != EstadoPlanNoReconocido || !cli[0].Listo {
			t.Fatalf("estado=%s listo=%v, esperaba plan_no_reconocido con listo=true",
				cli[0].Estado, cli[0].Listo)
		}
		if cli[0].PlanID.Valid || cli[0].Plan != "ONG 30 M" {
			t.Fatalf("plan_id=%+v plan=%q, esperaba plan_id vacío y el texto crudo",
				cli[0].PlanID, cli[0].Plan)
		}
	})

	t.Run("misma mac en 2 CT -> un solo cliente", func(t *testing.T) {
		f1 := filaOK(2, 5, "AA", "cli/1", 65)
		f2 := filaOK(2, 5, "aa", "cli/1", 65) // misma mac (case-insensitive)
		onus, cli := ClasificarPrimeraPasada([]FilaCarga{f1, f2}, 4, planes, gem)
		if len(cli) != 1 || onus[0].Estado != EstadoListo {
			t.Fatalf("esperaba 1 cliente y onu listo, got %d clientes, onu=%s", len(cli), onus[0].Estado)
		}
	})
}
