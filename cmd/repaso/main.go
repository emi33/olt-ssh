// Command repaso ejecuta la SEGUNDA PASADA del flujo de carga. Se corre DESPUÉS
// de `cargar` (la primera pasada). Reconstruye, por ONU de la OLT, el conjunto
// completo de MACs (histórico real + placeholders 00:00) y su cliente en CT, lo
// clasifica en los casos 1..10 y lo guarda en las tablas de registro con estado
// repaso_* (o sobrescrito_repaso_* si la 1ª pasada ya había tocado la posición).
// NO toca la OLT.
//
// Flags:
//
//	--dry-run   clasifica y muestra el resumen sin escribir en la BD.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sort"

	"oltssh/internal/config"
	"oltssh/internal/database"
	"oltssh/internal/spinner"
)

func main() { os.Exit(run()) }

func run() int {
	dryRun := false
	for _, a := range os.Args[1:] {
		if a == "--dry-run" || a == "-dry-run" {
			dryRun = true
		}
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Println("ERROR FATAL: " + err.Error())
		return 1
	}
	idOlt := cfg.Query.ProvisioningIDOlt

	fmt.Println("==============================================")
	fmt.Println("  Carga a tablas de registro — SEGUNDA PASADA (repaso)")
	fmt.Println("==============================================")
	fmt.Printf("  idOlt=%d  fecha_desde=%s  controladores=%v\n", idOlt, cfg.Query.FechaDesde, cfg.Query.Controladores)
	if dryRun {
		fmt.Println("  *** DRY-RUN: no se escribe en la BD ***")
	}
	fmt.Println()

	db, err := database.New(cfg.DB)
	if err != nil {
		fmt.Println("ERROR FATAL: " + err.Error())
		return 1
	}
	defer db.Close()
	ctx := context.Background()

	sp := spinner.Start("Consultando la base de datos (onu, histórico de macs, 00:00, 1ª pasada)")
	planes, err := db.GetPlanesPorNombre(ctx)
	var gem map[int]int
	var onusBD []database.OnuBasica
	var macsBD []database.MacRepaso
	var phBD map[[2]int][]int
	var principal map[[2]int]database.EstadoPrevio
	var verificadasBD []database.OnuVerificada
	if err == nil {
		gem, err = db.GetGemPortPorVlan(ctx, idOlt)
	}
	if err == nil {
		onusBD, err = db.GetOnusIdOlt(ctx, idOlt)
	}
	if err == nil {
		macsBD, err = db.GetMacsHistoricasRepaso(ctx, idOlt, cfg.Query.FechaDesde)
	}
	if err == nil {
		phBD, err = db.GetPlaceholderIdx(ctx, idOlt, cfg.Query.FechaDesde)
	}
	if err == nil {
		principal, err = db.GetPosicionesPrincipal(ctx, idOlt)
	}
	if err == nil {
		verificadasBD, err = db.GetOnusVerificadasTodasLasOlts(ctx)
	}
	sp.Stop()
	if err != nil {
		return fatal(err)
	}
	fmt.Printf("Fuentes: %d onus, %d macs histórico, %d posiciones con 00:00, %d posiciones ya cargadas por 1ª pasada, %d onus verificadas en todas las olts\n\n",
		len(onusBD), len(macsBD), len(phBD), len(principal), len(verificadasBD))

	// Ensamblar por posición.
	type pos = [2]int
	onuByPos := map[pos]database.OnuBasica{}
	for _, o := range onusBD {
		onuByPos[pos{o.Puerto, o.OntID}] = o
	}
	// Índice SN normalizado -> posiciones verificadas, de TODAS las OLTs. Sirve
	// para dos cosas sobre las ONUs sin tráfico: detectar el mismo equipo cargado
	// dos veces en esta OLT (serial escrito distinto), y detectar los que se
	// mudaron a otra OLT y por lo tanto no hay que migrar.
	verificadasPorSn := database.IndexarVerificadasPorSn(verificadasBD)
	// Resolver cada servicio (mac, vlan) contra sus matches en CT. La lógica vive
	// en database.ResolverMacs para poder testearla sin base de datos.
	macsByPos := database.ResolverMacs(macsBD, cfg.Query.Controladores)

	// Depuración por posición, antes de clasificar:
	//   1) se colapsan las entradas repetidas por (mac, vlan) o por idx, quedando
	//      la más reciente; sólo conviven los servicios genuinos de una misma MAC
	//      (distinta vlan Y distinto idx) — ver database.DedupMacs;
	//   2) un placeholder 00:00 cuyo idx ya ocupa una MAC real no aporta nada ->
	//      se descarta para no inflar el listado (los idx=0 los filtró la consulta).
	macsDesplazadas, phDescartados := 0, 0
	for p, macs := range macsByPos {
		conservadas, descartadas := database.DedupMacs(macs)
		macsByPos[p] = conservadas
		macsDesplazadas += len(descartadas)

		if idxs, hay := phBD[p]; hay {
			libres := database.PlaceholdersLibres(idxs, conservadas)
			phDescartados += len(idxs) - len(libres)
			if len(libres) == 0 {
				delete(phBD, p)
			} else {
				phBD[p] = libres
			}
		}
	}
	if macsDesplazadas > 0 || phDescartados > 0 {
		fmt.Printf("Depuración: %d macs descartadas por idx repetido (queda la más reciente), %d placeholders 00:00 descartados (idx ya ocupado)\n\n",
			macsDesplazadas, phDescartados)
	}

	// Universo de posiciones = onus ∪ macs ∪ placeholders.
	posSet := map[pos]bool{}
	for p := range onuByPos {
		posSet[p] = true
	}
	for p := range macsByPos {
		posSet[p] = true
	}
	for p := range phBD {
		posSet[p] = true
	}
	posiciones := make([]pos, 0, len(posSet))
	for p := range posSet {
		posiciones = append(posiciones, p)
	}
	sort.Slice(posiciones, func(i, j int) bool {
		if posiciones[i][0] != posiciones[j][0] {
			return posiciones[i][0] < posiciones[j][0]
		}
		return posiciones[i][1] < posiciones[j][1]
	})

	// Clasificar y guardar.
	resumen := map[string]int{}
	// listos: de cada estado, cuántas ONUs quedan con listo_para_cargar=1. Se
	// muestra al lado del total para poder ver en el dry-run qué se va a cargar.
	listos := map[string]int{}
	var onusOut []database.RegistroOnu
	var cliOut []database.RegistroCliente
	skips := 0
	for _, p := range posiciones {
		o := database.OnuRepaso{
			Puerto: p[0], OntID: p[1],
			Macs:           macsByPos[p],
			PlaceholderIdx: phBD[p],
		}
		// Estado con que la 1ª pasada dejó la posición: si el caso resulta ser el
		// feliz simple, se re-emite ESE estado en vez de asumir que era listo.
		if ep, ok := principal[p]; ok {
			o.YaPrincipal = true
			o.EstadoPrincipal = ep
		}
		if onu, existe := onuByPos[p]; existe {
			o.Sn = onu.Sn
			o.MacOnu = nullStr(onu.Mac)
			o.EstadoPresencia = nullStr(onu.EstadoPresencia)
			o.CantidadMacs = int(onu.CantidadMacs.Int64)
			// ¿El mismo SN (normalizado) está verificado en otra parte? Puede ser
			// en otra posición de esta OLT, o en otra OLT (equipo trasladado).
			if refs, hay := verificadasPorSn[database.NormalizarSn(nullStr(onu.Sn))]; hay {
				o.DuplicadoMismaOlt, o.DuplicadoOtraOlt =
					database.ResolverDuplicadoSn(refs, idOlt, p[0], p[1])
			}
		} else {
			o.SinOnu = true // caso 10: MAC(s) sin ONU en esa posición
		}

		onu, clientes, skip := database.ClasificarRepaso(o, idOlt, planes, gem)
		if skip {
			skips++
			continue
		}
		resumen[onu.Estado]++
		if onu.Listo {
			listos[onu.Estado]++
		}
		onusOut = append(onusOut, onu)
		cliOut = append(cliOut, clientes...)
	}

	fmt.Println("Resumen por estado (ONUs a escribir en el repaso):")
	imprimirResumen(resumen, listos)
	fmt.Printf("\nTotal repaso: %d ONUs, %d clientes. Ignoradas (ausente_sin_sn): %d\n",
		len(onusOut), len(cliOut), skips)

	if dryRun {
		fmt.Println("\nDry-run: no se guardó nada.")
		return 0
	}

	// Borrado + escritura en UNA transacción: la tabla queda exactamente con lo
	// que produjo esta corrida (sin restos de corridas anteriores que ensucien el
	// matcheo) y un corte a mitad de camino no la deja vacía. Incluye el relleno
	// final de las ONUs faltantes, para que registro_onu quede pareja con `onu`.
	sp2 := spinner.Start("Reemplazando registros del repaso (transacción)")
	res, err := db.ReemplazarRegistros(ctx, idOlt, onusOut, cliOut, true)
	sp2.Stop()
	if err != nil {
		fmt.Println("ERROR FATAL: " + err.Error())
		fmt.Println("La transacción se revirtió: las tablas quedaron como estaban.")
		return 1
	}
	fmt.Printf("Borradas %d ONUs y %d clientes de la corrida anterior; escritas %d ONUs y %d clientes.\n",
		res.BorradasOnu, res.BorradasCliente, len(onusOut), len(cliOut))
	if res.Rellenadas > 0 {
		fmt.Printf("Relleno: %d ONUs faltantes agregadas a registro_onu (estado 'repaso_relleno').\n", res.Rellenadas)
	}

	// Verificación de conteos.
	if cOnu, e1 := db.ContarOnu(ctx, idOlt); e1 == nil {
		if cReg, e2 := db.ContarRegistroOnu(ctx, idOlt); e2 == nil {
			fmt.Printf("Conteo: onu=%d  registro_onu=%d", cOnu, cReg)
			if cOnu == cReg {
				fmt.Println("  ✓ parejas")
			} else {
				fmt.Printf("  ✗ faltan %d (posible colisión de SN reales duplicados en onu)\n", cOnu-cReg)
			}
		}
	}

	fmt.Println("\nGuardado completo.")
	return 0
}

func fatal(err error) int {
	fmt.Println("ERROR FATAL: " + err.Error())
	return 1
}

func nullStr(s sql.NullString) string {
	if s.Valid {
		return s.String
	}
	return ""
}

// imprimirResumen lista cada estado con su total y, entre paréntesis, cuántas de
// esas ONUs quedan listas para cargar. Ver el listo acá evita tener que ir a la
// BD para saber qué va a levantar cmd/provisionar después del repaso.
func imprimirResumen(m, listos map[string]int) {
	claves := make([]string, 0, len(m))
	for k := range m {
		claves = append(claves, k)
	}
	sort.Strings(claves)
	for _, k := range claves {
		fmt.Printf("  %-45s %4d  (listos: %d)\n", k, m[k], listos[k])
	}
}
