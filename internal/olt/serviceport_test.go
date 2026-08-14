package olt

import "testing"

// capturaRealShowServicePort es la salida real capturada en la consola de
// la OLT DS-P7001-16_53FBD0 (ver docs/08-comandos-olt.md).
const capturaRealShowServicePort = `Index PON-Port   ONT-ID  GEM-ID  SVLAN   User-VLAN/Pri   EtherType   TAG-Action      Inner-VLAN/Pri  Inbound-traffic   Outbound-traffic  Active-status  Admin-status
3698  GPON1/1/16 6       1       666     666/-           ---         transparent     -/-             ---               ---               Inactive       Enable
3699  GPON1/1/1  2       2       1       1/-             ---         default         -/-             6                 6                 Inactive       Enable

DS-P7001-16_53FBD0(config)#`

func TestParseIndicesServicePort_CapturaReal(t *testing.T) {
	ocupados := parseIndicesServicePort(capturaRealShowServicePort)

	esperados := map[int]bool{3698: true, 3699: true}
	if len(ocupados) != len(esperados) {
		t.Fatalf("se esperaban %d indices, se obtuvieron %d: %v", len(esperados), len(ocupados), ocupados)
	}
	for idx := range esperados {
		if !ocupados[idx] {
			t.Errorf("falta el indice %d en el resultado: %v", idx, ocupados)
		}
	}
}

func TestParseIndicesServicePort_DescartaEncabezado(t *testing.T) {
	ocupados := parseIndicesServicePort(capturaRealShowServicePort)
	// La fila "Index PON-Port ..." no debe generar ninguna entrada — "Index"
	// no matchea el regex numérico.
	if len(ocupados) != 2 {
		t.Fatalf("se coló una fila que no era de datos: %v", ocupados)
	}
}

func TestParseIndicesServicePort_TablaVacia(t *testing.T) {
	vacio := "Index PON-Port   ONT-ID  GEM-ID  SVLAN ...\n\nDS-P7001-16_53FBD0(config)#"
	ocupados := parseIndicesServicePort(vacio)
	if len(ocupados) != 0 {
		t.Fatalf("se esperaba un set vacio, se obtuvo: %v", ocupados)
	}
}
