package olt

import "testing"

func contieneTodos(indices []int, esperados ...int) bool {
	set := make(map[int]bool, len(indices))
	for _, i := range indices {
		set[i] = true
	}
	for _, e := range esperados {
		if !set[e] {
			return false
		}
	}
	return true
}

func TestSiguienteIndicesLibres_SetVacioUsaBase(t *testing.T) {
	indices := SiguienteIndicesLibres(map[int]bool{}, 3, 3697)
	if !contieneTodos(indices, 3697, 3698, 3699) || len(indices) != 3 {
		t.Fatalf("esperaba [3697 3698 3699], obtuve %v", indices)
	}
}

func TestSiguienteIndicesLibres_ArrancaDespuesDelMaximo(t *testing.T) {
	ocupados := map[int]bool{3698: true, 3699: true}
	indices := SiguienteIndicesLibres(ocupados, 2, 3697)
	if !contieneTodos(indices, 3700, 3701) || len(indices) != 2 {
		t.Fatalf("esperaba [3700 3701], obtuve %v", indices)
	}
}

func TestSiguienteIndicesLibres_SaltaHuecosOcupados(t *testing.T) {
	// Caso defensivo: aunque el punto de partida sea max+1, si por alguna
	// inconsistencia ese valor tambien figura en 'ocupados', se salta.
	ocupados := map[int]bool{3698: true, 3699: true, 3700: true}
	indices := SiguienteIndicesLibres(ocupados, 1, 3697)
	if !contieneTodos(indices, 3701) || len(indices) != 1 {
		t.Fatalf("esperaba [3701], obtuve %v", indices)
	}
}

func TestSiguienteIndicesLibres_MaximoAlto(t *testing.T) {
	ocupados := map[int]bool{9999: true}
	indices := SiguienteIndicesLibres(ocupados, 1, 3697)
	if !contieneTodos(indices, 10000) || len(indices) != 1 {
		t.Fatalf("esperaba [10000], obtuve %v", indices)
	}
}

func TestSiguienteIndicesLibres_CantidadCero(t *testing.T) {
	if indices := SiguienteIndicesLibres(map[int]bool{}, 0, 3697); len(indices) != 0 {
		t.Fatalf("esperaba slice vacio, obtuve %v", indices)
	}
}
