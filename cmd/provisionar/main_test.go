package main

import (
	"database/sql"
	"strings"
	"testing"

	"oltssh/internal/database"
)

// reg arma un RegistroServicio mínimo pero válido para seleccionarPendientes.
func reg(id int64, vlan int, plan, estado string) database.RegistroServicio {
	return database.RegistroServicio{
		ID: id, Mac: "AA:BB:CC:DD:EE:FF", PuertoOlt: 3, Identificador: 7, Vlan: vlan,
		Sn:             sql.NullString{String: "TPLGB7329D40", Valid: true},
		Plan:           sql.NullString{String: plan, Valid: plan != ""},
		NroConexion:    sql.NullString{String: "10235", Valid: true},
		GemId:          sql.NullInt64{Int64: 1, Valid: true},
		EstadoRegistro: sql.NullString{String: estado, Valid: true},
	}
}

// TestPlanNoReconocidoSinTrafico fija el contrato del plan NULL: las filas con
// estado 'plan no reconocido' se cargan igual, pero el service-port sale SIN
// traffic-in/out (el plan se carga a mano en el equipo). Mismo criterio que el
// 'desc' vacío: el parámetro sin dato no se manda, porque mandarlo vacío hace
// que la OLT rechace el comando entero.
func TestPlanNoReconocidoSinTrafico(t *testing.T) {
	casos := []struct {
		nombre     string
		registros  []database.RegistroServicio
		contiene   string
		noContiene string
	}{
		{
			nombre:     "plan no reconocido -> sin traffic-in/out",
			registros:  []database.RegistroServicio{reg(1, 65, "6 MB Hogar", "principal_plan_no_reconocido")},
			contiene:   "tag-action transparent desc 10235",
			noContiene: "traffic-in",
		},
		{
			nombre:     "prefijo repaso_ tambien cuenta",
			registros:  []database.RegistroServicio{reg(1, 65, "ONG", "sobrescrito_repaso_plan_no_reconocido")},
			contiene:   "tag-action transparent desc 10235",
			noContiene: "traffic-in",
		},
		{
			nombre:     "plan reconocido -> conserva traffic-in/out",
			registros:  []database.RegistroServicio{reg(1, 65, "200 MB Hogar", "principal_listo")},
			contiene:   "tag-action transparent traffic-in 2 traffic-out 2 desc 10235",
			noContiene: "~nada~",
		},
		{
			nombre: "misma ONU: un plan reconocido gana sobre el nulo",
			registros: []database.RegistroServicio{
				reg(1, 65, "6 MB Hogar", "principal_plan_no_reconocido"),
				reg(2, 40, "300 MB Hogar", "principal_listo"),
			},
			contiene:   "traffic-in 3 traffic-out 3",
			noContiene: "~nada~",
		},
	}

	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			pend, omit := seleccionarPendientes(c.registros)
			if len(omit) != 0 {
				t.Fatalf("no esperaba omitidos, hubo %d: %v", len(omit), omit)
			}
			if len(pend) != 1 {
				t.Fatalf("esperaba 1 entrada, hubo %d", len(pend))
			}
			for _, sp := range pend[0].sps {
				t.Logf("service-port <auto> %s", sp.sufijo)
				if !strings.Contains(sp.sufijo, c.contiene) {
					t.Errorf("el comando no contiene %q", c.contiene)
				}
				if strings.Contains(sp.sufijo, c.noContiene) {
					t.Errorf("el comando NO debería contener %q", c.noContiene)
				}
			}
		})
	}
}
