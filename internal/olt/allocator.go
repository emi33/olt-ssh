package olt

// SiguienteIndicesLibres calcula 'cantidad' índices de service-port libres,
// partiendo de max(ocupados)+1 (o 'base' si 'ocupados' está vacío — OLT sin
// ningún service-port creado todavía). Salta cualquier valor que igual esté
// en 'ocupados': no debería disparar nunca si el máximo se calculó bien,
// pero cubre huecos/inconsistencias entre lo cacheado y lo real.
func SiguienteIndicesLibres(ocupados map[int]bool, cantidad int, base int) []int {
	if cantidad <= 0 {
		return nil
	}

	siguiente := base
	for idx := range ocupados {
		if idx+1 > siguiente {
			siguiente = idx + 1
		}
	}

	indices := make([]int, 0, cantidad)
	for len(indices) < cantidad {
		if !ocupados[siguiente] {
			indices = append(indices, siguiente)
		}
		siguiente++
	}
	return indices
}
