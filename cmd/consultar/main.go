// Command consultar se conecta a una OLT via SSH y ejecuta solo comandos show
// (lectura). Antes de cada comando muestra que va a ejecutar y pide confirmacion.
// No modifica nada en la OLT.
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
)

func main() {
	os.Exit(run())
}

func run() int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Println("ERROR FATAL: " + err.Error())
		return 1
	}

	fmt.Println("==============================================")
	fmt.Println("  Consulta de OLT (solo lectura - show)")
	fmt.Println("==============================================")
	fmt.Printf("  OLT destino: %s:%d\n", cfg.OLT.Host, cfg.OLT.Port)
	fmt.Printf("  Base de datos: %s@%s:%d/%s\n", cfg.DB.Username, cfg.DB.Host, cfg.DB.Port, cfg.DB.Name)
	fmt.Println()

	// --- Base de datos ---
	fmt.Println("Conectando a la base de datos...")
	db, err := database.New(cfg.DB)
	if err != nil {
		fmt.Println("ERROR FATAL: " + err.Error())
		return 1
	}
	defer db.Close()

	fmt.Printf("Consultando ONUs de OLT %d, puerto %d...\n", cfg.Query.IDOlt, cfg.Query.PuertoOlt)
	onus, err := db.GetONUsDelPuerto(context.Background(), cfg.Query.IDOlt, cfg.Query.PuertoOlt)
	if err != nil {
		fmt.Println("ERROR FATAL: " + err.Error())
		return 1
	}
	fmt.Printf("Se encontraron %d ONUs en la base de datos.\n\n", len(onus))

	// --- Comandos show a ejecutar ---
	comandos := buildShowCommands(cfg.Query.PuertoOlt, onus)

	fmt.Println("Comandos que se ejecutaran (todos son show, solo lectura):")
	fmt.Println("--------------------------------------------")
	for i, c := range comandos {
		fmt.Printf("  [%d] %s\n", i+1, c)
	}
	fmt.Println("--------------------------------------------")
	fmt.Printf("Total: %d comandos\n\n", len(comandos))

	// --- Conexion SSH ---
	fmt.Print("¿Conectar a la OLT? (s/N): ")
	if !confirmar() {
		fmt.Println("Operacion cancelada.")
		return 0
	}

	log, err := logger.New("logs")
	if err != nil {
		fmt.Println("ERROR FATAL: " + err.Error())
		return 1
	}
	defer log.Close()
	fmt.Println("Log: " + log.Path())

	conn := olt.New(cfg.OLT, log)

	fmt.Println("\nConectando a la OLT...")
	if err := conn.Connect(); err != nil {
		fmt.Println("ERROR: " + err.Error())
		return 1
	}
	defer conn.Disconnect()

	fmt.Println("Entrando en modo enable...")
	if err := conn.EnableMode(); err != nil {
		fmt.Println("ERROR: " + err.Error())
		return 1
	}

	// --- Ejecucion con confirmacion por comando ---
	fmt.Println()
	ejecutados := 0
	for i, cmd := range comandos {
		fmt.Printf("\n[%d/%d] Proximo comando:\n", i+1, len(comandos))
		fmt.Printf("  >>> %s\n", cmd)
		fmt.Print("  ¿Ejecutar? (s/N/todos/salir): ")

		opcion := leerOpcion()

		switch opcion {
		case "salir":
			fmt.Println("  Detenido por el usuario.")
			goto fin
		case "todos":
			resp, err := conn.ExecuteCommand(cmd)
			if err != nil {
				fmt.Printf("  ERROR: %v\n", err)
				goto fin
			}
			printRespuesta(resp)
			ejecutados++

			for j := i + 1; j < len(comandos); j++ {
				fmt.Printf("\n[%d/%d] Ejecutando: %s\n", j+1, len(comandos), comandos[j])
				resp, err := conn.ExecuteCommand(comandos[j])
				if err != nil {
					fmt.Printf("  ERROR: %v\n", err)
					goto fin
				}
				printRespuesta(resp)
				ejecutados++
			}
			goto fin
		case "s":
			resp, err := conn.ExecuteCommand(cmd)
			if err != nil {
				fmt.Printf("  ERROR: %v\n", err)
				goto fin
			}
			printRespuesta(resp)
			ejecutados++
		default:
			fmt.Println("  Saltado.")
		}
	}

fin:
	fmt.Println("\n==============================================")
	fmt.Printf("  Comandos ejecutados: %d/%d\n", ejecutados, len(comandos))
	fmt.Printf("  Log: %s\n", log.Path())
	fmt.Println("==============================================")
	return 0
}

// buildShowCommands genera la lista de comandos show para el puerto.
func buildShowCommands(puertoOlt int, onus []database.ONUInfo) []string {
	var cmds []string
	cmds = append(cmds, fmt.Sprintf("show ont info gpon 1/1/%d", puertoOlt))
	cmds = append(cmds, fmt.Sprintf("show ont info gpon 1/1/%d detail", puertoOlt))
	cmds = append(cmds, fmt.Sprintf("show ont optical-info gpon 1/1/%d", puertoOlt))
	cmds = append(cmds, fmt.Sprintf("show ont version gpon 1/1/%d", puertoOlt))
	cmds = append(cmds, fmt.Sprintf("show ont auth-info gpon 1/1/%d", puertoOlt))
	return cmds
}

func printRespuesta(resp string) {
	resp = strings.TrimSpace(resp)
	if resp == "" {
		fmt.Println("  (sin respuesta)")
		return
	}
	fmt.Println("  ─── RESPUESTA ───")
	for _, line := range strings.Split(resp, "\n") {
		fmt.Printf("  %s\n", line)
	}
	fmt.Println("  ─── FIN ───")
}

func confirmar() bool {
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	return strings.ToLower(strings.TrimSpace(line)) == "s"
}

func leerOpcion() string {
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	return strings.ToLower(strings.TrimSpace(line))
}
