// Command cargar ejecuta la PRIMERA PASADA del flujo de carga a las tablas de
// registro (registro_onu / registro_cliente): corre la consulta principal
// (clientes activos en los CT concentradores), clasifica cada fila en un estado
// y guarda los registros. NO toca la OLT.
//
// Flags:
//
//	--dry-run   clasifica y muestra el resumen sin escribir en la BD.
package main

import (
	"context"
	"fmt"
	"os"
	"sort"

	"oltssh/internal/config"
	"oltssh/internal/database"
	"oltssh/internal/spinner"
)

func main() {
	os.Exit(run())
}

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
	fmt.Println("  Carga a tablas de registro — PRIMERA PASADA")
	fmt.Println("==============================================")
	fmt.Printf("  idOlt=%d  fecha_desde=%s\n", idOlt, cfg.Query.FechaDesde)
	fmt.Printf("  controladores=%v\n", cfg.Query.Controladores)
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

	sp := spinner.Start("Consultando la base de datos")
	planes, err := db.GetPlanesPorNombre(ctx)
	var gem map[int]int
	var filas []database.FilaCarga
	if err == nil {
		gem, err = db.GetGemPortPorVlan(ctx, idOlt)
	}
	if err == nil {
		filas, err = db.GetFilasCargaPrincipal(ctx, idOlt, cfg.Query.FechaDesde, cfg.Query.Controladores)
	}
	sp.Stop()
	if err != nil {
		fmt.Println("ERROR FATAL: " + err.Error())
		return 1
	}
	fmt.Printf("Referencia: %d planes, %d vlans en gem_config | Filas de la consulta principal: %d\n\n",
		len(planes), len(gem), len(filas))

	onus, clientes := database.ClasificarPrimeraPasada(filas, idOlt, planes, gem)

	// Resumen por estado.
	fmt.Println("Resumen por estado (ONUs):")
	imprimirResumen(estadosOnu(onus))
	fmt.Println("\nResumen por estado (Clientes):")
	imprimirResumen(estadosCliente(clientes))
	fmt.Printf("\nTotal: %d ONUs, %d clientes. Listos para cargar: %d ONUs, %d clientes.\n",
		len(onus), len(clientes), contarListosOnu(onus), contarListosCliente(clientes))

	if dryRun {
		fmt.Println("\nDry-run: no se guardó nada.")
		return 0
	}

	// Borrado + escritura en UNA transacción: la primera pasada arranca de cero
	// para esta OLT, así que lo que quedó de corridas anteriores se borra antes de
	// escribir (si no, seguirían vivas MACs y clientes que ya no existen, y
	// contaminarían el matcheo). En transacción para que un corte a mitad no deje
	// las tablas borradas y sin cargar: o entra todo lo nuevo, o sigue lo viejo.
	sp2 := spinner.Start("Reemplazando registros (transacción)")
	res, err := db.ReemplazarRegistros(ctx, idOlt, onus, clientes, false)
	sp2.Stop()
	if err != nil {
		fmt.Println("ERROR FATAL: " + err.Error())
		fmt.Println("La transacción se revirtió: las tablas quedaron como estaban.")
		return 1
	}
	fmt.Printf("\nGuardado completo. Borradas %d ONUs y %d clientes de la corrida anterior; escritas %d ONUs y %d clientes.\n",
		res.BorradasOnu, res.BorradasCliente, len(onus), len(clientes))
	return 0
}

func estadosOnu(onus []database.RegistroOnu) map[string]int {
	m := map[string]int{}
	for _, o := range onus {
		m[o.Estado]++
	}
	return m
}

func estadosCliente(cli []database.RegistroCliente) map[string]int {
	m := map[string]int{}
	for _, c := range cli {
		m[c.Estado]++
	}
	return m
}

func contarListosOnu(onus []database.RegistroOnu) (n int) {
	for _, o := range onus {
		if o.Listo {
			n++
		}
	}
	return n
}

func contarListosCliente(cli []database.RegistroCliente) (n int) {
	for _, c := range cli {
		if c.Listo {
			n++
		}
	}
	return n
}

func imprimirResumen(m map[string]int) {
	claves := make([]string, 0, len(m))
	for k := range m {
		claves = append(claves, k)
	}
	sort.Strings(claves)
	for _, k := range claves {
		fmt.Printf("  %-45s %d\n", k, m[k])
	}
}
