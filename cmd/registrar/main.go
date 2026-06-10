// Command registrar es el punto de entrada CLI: carga la configuración, obtiene
// los comandos de la base de datos y, según el modo, los simula (--dry-run) o los
// ejecuta en la OLT. Equivale a registrar_clientes.php.
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"oltssh/internal/config"
	"oltssh/internal/database"
	"oltssh/internal/logger"
	"oltssh/internal/olt"
	"oltssh/internal/registrar"
)

func main() {
	os.Exit(run())
}

// run contiene la lógica principal y devuelve el código de salida.
// (Se separa de main para poder usar defer sin perder el código de retorno.)
func run() int {
	// --- Flags ---
	dryRun := false
	for _, a := range os.Args[1:] {
		if a == "--dry-run" || a == "-dry-run" {
			dryRun = true
		}
	}

	fmt.Println("==============================================")
	fmt.Println("  Registro de Clientes en OLT")
	fmt.Println("==============================================")
	fmt.Println()

	if dryRun {
		fmt.Println("*** MODO DRY-RUN: Los comandos NO serán ejecutados ***")
		fmt.Println()
	}

	// --- Configuración ---
	cfg, err := config.Load()
	if err != nil {
		fmt.Println("ERROR FATAL: " + err.Error())
		return 1
	}

	// --- Base de datos ---
	fmt.Println("Conectando a la base de datos...")
	db, err := database.New(cfg.DB)
	if err != nil {
		fmt.Println("ERROR FATAL: " + err.Error())
		return 1
	}
	defer db.Close()

	fmt.Println("Obteniendo comandos de registro...")
	comandos, err := db.GetComandosRegistro(context.Background(), cfg.Query.IDOlt, cfg.Query.PuertoOlt)
	if err != nil {
		fmt.Println("ERROR FATAL: " + err.Error())
		return 1
	}

	if len(comandos) == 0 {
		fmt.Println("No se encontraron ONTs para registrar.")
		return 0
	}

	fmt.Printf("Se encontraron %d ONTs para registrar.\n\n", len(comandos))

	// --- Previsualización ---
	printComandos(comandos)

	// --- Dry-run: volcar al log y terminar ---
	if dryRun {
		if err := writeDryRunLog(cfg, comandos); err != nil {
			fmt.Println("ERROR FATAL: " + err.Error())
			return 1
		}
		return 0
	}

	// --- Confirmación ---
	fmt.Print("\n¿Desea ejecutar estos comandos en la OLT? (s/N): ")
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	if strings.ToLower(strings.TrimSpace(line)) != "s" {
		fmt.Println("Operación cancelada.")
		return 0
	}

	// --- Ejecución real ---
	fmt.Println("\nIniciando registro de clientes...")
	fmt.Println()

	log, err := logger.New("logs")
	if err != nil {
		fmt.Println("ERROR FATAL: " + err.Error())
		return 1
	}
	fmt.Println("Log guardado en: " + log.Path())
	fmt.Println()

	conn := olt.New(cfg.OLT, log)
	reg := registrar.New(conn, log, comandos, cfg.Query.PuertoOlt)

	res := reg.Run()
	log.Close()

	printResultados(res, log.Path())

	if res.Success {
		return 0
	}
	return 1
}

// printComandos muestra en pantalla los dos pasos de comandos.
func printComandos(comandos []database.Comando) {
	fmt.Println("Comandos a ejecutar:")
	fmt.Println("--------------------------------------------")

	fmt.Println("\n### PASO 1: Comandos ONT ADD (en interface gpon) ###")
	for i, c := range comandos {
		fmt.Printf("[%d] %s\n", i+1, c.Comando2)
	}

	fmt.Println("\n### PASO 2: Comandos SERVICE-PORT (en config) ###")
	for i, c := range comandos {
		fmt.Printf("[%d] %s\n", i+1, c.Comando1)
	}

	fmt.Println("\n--------------------------------------------")
}

// writeDryRunLog vuelca al log la información del dry-run sin tocar la OLT.
func writeDryRunLog(cfg *config.Config, comandos []database.Comando) error {
	log, err := logger.New("logs")
	if err != nil {
		return err
	}
	defer log.Close()

	log.Info("MODO DRY-RUN - Comandos NO ejecutados")
	log.Info("OLT: " + cfg.OLT.Host)
	log.Info(fmt.Sprintf("Puerto GPON: 1/1/%d", cfg.Query.PuertoOlt))
	log.Info(fmt.Sprintf("Total ONTs: %d", len(comandos)))

	log.Write("")
	log.Write("### COMANDOS ONT ADD ###")
	for _, c := range comandos {
		log.Write(c.Comando2)
	}
	log.Write("")
	log.Write("### COMANDOS SERVICE-PORT ###")
	for _, c := range comandos {
		log.Write(c.Comando1)
	}

	fmt.Println("\nLog guardado en: " + log.Path())
	fmt.Println("Modo dry-run finalizado. Use sin --dry-run para ejecutar.")
	return nil
}

// printResultados imprime el resumen final del proceso.
func printResultados(res registrar.Result, logPath string) {
	fmt.Println("\n==============================================")
	fmt.Println("  RESULTADOS")
	fmt.Println("==============================================")
	fmt.Printf("Comandos ejecutados: %d\n", res.ComandosEjecutados)
	fmt.Printf("Errores encontrados: %d\n", len(res.Errores))
	estado := "ÉXITO"
	if !res.Success {
		estado = "CON ERRORES"
	}
	fmt.Printf("Estado: %s\n", estado)
	fmt.Printf("Log: %s\n", logPath)

	if len(res.Errores) > 0 {
		fmt.Println("\nErrores:")
		for _, e := range res.Errores {
			if e.Comando != "" {
				fmt.Printf("  - Comando: %s\n", e.Comando)
				fmt.Printf("    Respuesta: %s\n", e.Respuesta)
			} else {
				fmt.Printf("  - %s\n", e.Mensaje)
			}
		}
	}
}
