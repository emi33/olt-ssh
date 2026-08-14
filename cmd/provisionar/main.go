// Command provisionar lee los registros de clientes activos desde la BD (tabla
// observacionesct cruzada con eqcliente/conexiones/onu) y ejecuta comandos en la
// OLT. Tiene dos operaciones sobre el MISMO conjunto de clientes filtrado:
//
//	(default)         crea los 'service-port' (asocia VLAN/plan a cada ONT).
//	--registrar-onu   da de alta las ONTs con 'ont add' (registro por SN).
//
// Ambas parten de seleccionarPendientes(), que aplica exactamente los mismos
// filtros (estado de presencia de la ONU + plan reconocido + SN presente), de
// modo que la cantidad de 'ont add' y de 'service-port' generados coincide
// siempre.
//
// El índice de cada service-port se asigna en vivo, después de conectar,
// consultando 'show service-port' para no colisionar con índices ya usados
// en la OLT (ver internal/olt/serviceport.go).
//
// Flags:
//
//	--dry-run        muestra los comandos y escribe el log sin tocar la OLT
//	--registrar-onu  ejecuta el alta de ONTs ('ont add') en vez de los service-port
//	--sp-inicio=N    base de índice a usar SOLO si la OLT no tiene ningún
//	                 service-port creado todavía (default: env
//	                 PROVISIONING_SP_INICIO o 3699); con service-ports
//	                 existentes el índice real siempre arranca en
//	                 max(ocupados)+1, este flag no aplica.
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"oltssh/internal/config"
	"oltssh/internal/database"
	"oltssh/internal/logger"
	"oltssh/internal/olt"
)

func main() {
	os.Exit(run())
}

// servicePort es un 'service-port' a crear: una VLAN de una ONT. La OLT no
// acepta varias VLANs en un mismo service-port, así que hay uno por VLAN.
type servicePort struct {
	vlan   int     // VLAN del servicio
	sufijo string  // comando sin índice: 'config gpon ...'
	ids    []int64 // filas de registro_cliente que cubre, para escribirles sp_index
	num    int     // índice asignado en vivo al conectar (0 hasta entonces)
}

// entrada es un cliente que pasó todos los filtros, con los comandos ya
// armados (el 'ont add' y un service-port por cada VLAN de la ONT).
type entrada struct {
	reg        database.RegistroServicio
	sps        []servicePort // uno por VLAN de la ONT
	comandoOnt string        // 'ont add ...' completo
	tc         int           // código de tráfico (traffic-in/out)
}

func run() int {
	// --- Flags ---
	dryRun := false
	registrarOnu := false
	spInicio := spInicioDesdeEnv()
	for _, a := range os.Args[1:] {
		switch {
		case a == "--dry-run" || a == "-dry-run":
			dryRun = true
		case a == "--registrar-onu" || a == "-registrar-onu":
			registrarOnu = true
		case strings.HasPrefix(a, "--sp-inicio="):
			if n, err := strconv.Atoi(strings.TrimPrefix(a, "--sp-inicio=")); err == nil {
				spInicio = n
			}
		}
	}

	operacion := "Provisioning de Service-Port"
	if registrarOnu {
		operacion = "Registro de ONTs (ont add)"
	}

	fmt.Println("==============================================")
	fmt.Printf("  %s en OLT\n", operacion)
	fmt.Println("==============================================")
	fmt.Println()
	if dryRun {
		fmt.Println("*** MODO DRY-RUN: Los comandos NO serán ejecutados en la OLT ***")
		fmt.Println()
	}
	if !registrarOnu {
		fmt.Printf("  Base de índice SP (solo si la OLT no tiene ninguno): %d\n\n", spInicio)
	}

	// --- Config ---
	cfg, err := config.Load()
	if err != nil {
		fmt.Println("ERROR FATAL: " + err.Error())
		return 1
	}
	fmt.Printf("  OLT destino: %s:%d\n", cfg.OLT.Host, cfg.OLT.Port)
	fmt.Printf("  Base de datos: %s@%s:%d/%s\n\n", cfg.DB.Username, cfg.DB.Host, cfg.DB.Port, cfg.DB.Name)

	// --- Base de datos ---
	fmt.Println("Conectando a la base de datos...")
	db, err := database.New(cfg.DB)
	if err != nil {
		fmt.Println("ERROR FATAL: " + err.Error())
		return 1
	}
	defer db.Close()

	// --- Guardrail: verificar que OLT_HOST sea la OLT del idOlt configurado ---
	// PROVISIONING_ID_OLT selecciona los clientes en la BD; OLT_HOST decide a
	// qué equipo se conecta el SSH. Si no coinciden, se configuraría una OLT
	// distinta a la del idOlt. Comparamos OLT_HOST con la ipAdmin de la tabla
	// `olt` para ese idOlt y abortamos ante una discrepancia.
	if code := verificarOLTDestino(db, cfg); code != 0 {
		return code
	}

	fmt.Printf("Ejecutando consulta de provisioning (idOlt=%d)...\n", cfg.Query.ProvisioningIDOlt)
	registros, err := db.GetRegistrosProvisioning(context.Background(), cfg.Query.ProvisioningIDOlt)
	if err != nil {
		fmt.Println("ERROR FATAL: " + err.Error())
		return 1
	}
	fmt.Printf("Registros obtenidos de la BD: %d\n\n", len(registros))

	// --- Filtrado común (estado de presencia + plan + SN) ---
	// El mismo conjunto alimenta las dos operaciones, así que la cantidad de
	// 'ont add' y de 'service-port' es idéntica.
	pendientes, omitidos := seleccionarPendientes(registros)

	// --- Resumen de omitidos (equipos no cargados) ---
	if len(omitidos) > 0 {
		fmt.Printf("Equipos NO cargados (estado no cargable, sin plan reconocido o sin SN): %d\n", len(omitidos))
		for _, o := range omitidos {
			fmt.Println("  " + o)
		}
		fmt.Println()
	}

	if len(pendientes) == 0 {
		fmt.Println("No hay registros válidos para procesar.")
		return 0
	}

	// --- Previsualización ---
	fmt.Printf("Comandos a ejecutar en OLT (%d registros):\n", len(pendientes))
	fmt.Println("--------------------------------------------")
	for i, e := range pendientes {
		plan := e.reg.Plan.String
		if registrarOnu {
			fmt.Printf("[%d] MAC=%-20s plan=%-20s\n    %s\n", i+1, e.reg.Mac, plan, e.comandoOnt)
		} else {
			// El índice todavía no se conoce (se asigna al conectar), se
			// muestra "<auto>" para no sugerir un número que puede no ser el real.
			traffic := strconv.Itoa(e.tc)
			if e.tc <= 0 {
				traffic = "NULL (plan no reconocido)"
			}
			fmt.Printf("[%d] MAC=%-20s plan=%-20s traffic=%s\n", i+1, e.reg.Mac, plan, traffic)
			for _, sp := range e.sps {
				fmt.Printf("    service-port <auto> %s\n", sp.sufijo)
			}
		}
	}
	fmt.Println("--------------------------------------------")
	fmt.Println()

	// --- Logger ---
	log, err := logger.New("logs")
	if err != nil {
		fmt.Println("ERROR FATAL creando log: " + err.Error())
		return 1
	}
	defer log.Close()
	fmt.Println("Log: " + log.Path())
	fmt.Println()

	log.Info("Operación: " + operacion)
	log.Info(fmt.Sprintf("OLT destino: %s:%d", cfg.OLT.Host, cfg.OLT.Port))
	log.Info(fmt.Sprintf("Base de datos: %s@%s:%d/%s", cfg.DB.Username, cfg.DB.Host, cfg.DB.Port, cfg.DB.Name))
	log.Info(fmt.Sprintf("Registros en BD: %d | A procesar: %d | Omitidos: %d", len(registros), len(pendientes), len(omitidos)))

	// Lista de equipos que NO se cargaron y por qué, para poder revisar qué
	// pasó con cada uno (estado no cargable, plan no reconocido o SN vacío).
	if len(omitidos) > 0 {
		log.Write("")
		log.Write(fmt.Sprintf("=== EQUIPOS NO CARGADOS (%d) — no cumplieron las condiciones ===", len(omitidos)))
		for _, o := range omitidos {
			log.Write(o)
		}
	}
	log.Write("")
	log.Write("=== COMANDOS A EJECUTAR ===")
	for _, e := range pendientes {
		if registrarOnu {
			log.Write(e.comandoOnt)
		} else {
			for _, sp := range e.sps {
				log.Write("service-port <auto> " + sp.sufijo)
			}
		}
	}
	log.Write("")

	// --- Dry-run: terminar sin tocar OLT ---
	if dryRun {
		log.Info("MODO DRY-RUN - OLT no modificada")
		fmt.Println("Modo dry-run: log escrito. Use sin --dry-run para ejecutar en OLT.")
		return 0
	}

	// --- Confirmación ---
	fmt.Print("¿Ejecutar estos comandos en la OLT? (s/N): ")
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	if strings.ToLower(strings.TrimSpace(line)) != "s" {
		log.Info("Operación cancelada por el usuario")
		fmt.Println("Operación cancelada.")
		return 0
	}

	// --- Conexión SSH ---
	conn := olt.New(cfg.OLT, log)

	log.Info("Iniciando conexión SSH...")
	fmt.Println("\nConectando a la OLT...")
	if err := conn.Connect(); err != nil {
		log.Error("Conexión SSH fallida: " + err.Error())
		fmt.Println("ERROR: " + err.Error())
		return 1
	}
	defer conn.Disconnect()

	fmt.Println("Entrando en modo enable...")
	if err := conn.EnableMode(); err != nil {
		log.Error("Enable mode fallido: " + err.Error())
		fmt.Println("ERROR: " + err.Error())
		return 1
	}

	// Best-effort: callar las notificaciones asincrónicas del firmware (logins,
	// alarmas). Si se imprimen mientras se tipea un comando pisan el eco y la OLT
	// lo lee incompleto ('Bad command'). Si el firmware no acepta estos comandos
	// responde con un error que se ignora.
	for _, cmd := range []string{"undo terminal monitor", "undo terminal debugging"} {
		if _, err := conn.ExecuteCommand(cmd); err != nil {
			log.Info(fmt.Sprintf("'%s' no se pudo enviar: %v", cmd, err))
		}
	}

	fmt.Println("Entrando en modo configuración...")
	if _, err := conn.ExecuteCommand("configure"); err != nil {
		log.Error("configure fallido: " + err.Error())
		fmt.Println("ERROR: " + err.Error())
		return 1
	}

	var ejecutados int
	var erroresDetalle []string
	var asignaciones []database.AsignacionSP
	if registrarOnu {
		ejecutados, erroresDetalle = ejecutarRegistroOnu(conn, log, pendientes)
	} else {
		ejecutados, erroresDetalle, asignaciones = ejecutarServicePorts(conn, log, pendientes, spInicio)
	}
	errores := len(erroresDetalle)

	// --- Guardar configuración ---
	if ejecutados > 0 {
		fmt.Println("\nGuardando configuración en OLT...")
		if err := conn.SaveConfig(); err != nil {
			log.Error("save fallido: " + err.Error())
			fmt.Println("ADVERTENCIA: error al guardar config: " + err.Error())
		} else {
			log.Info("Configuración guardada correctamente")
		}
	}

	// --- Registrar en la BD los índices que quedaron creados ---
	// Se hace después de ejecutar (y no antes) para que sp_index refleje lo que
	// la OLT aceptó de verdad. Un fallo acá no invalida la corrida: los
	// service-port ya están en el equipo, así que se avisa y se sigue.
	if len(asignaciones) > 0 {
		filas := 0
		for _, a := range asignaciones {
			filas += len(a.IDs)
		}
		fmt.Printf("\nGuardando sp_index en registro_cliente (%d filas)...\n", filas)
		actualizadas, err := db.GuardarSPIndex(context.Background(), asignaciones)
		if err != nil {
			log.Error("guardando sp_index: " + err.Error())
			fmt.Println("ADVERTENCIA: no se pudo guardar sp_index: " + err.Error())
		} else {
			msg := fmt.Sprintf("sp_index guardado en registro_cliente: %d service-port -> %d filas actualizadas",
				len(asignaciones), actualizadas)
			fmt.Println("  " + msg)
			log.Info(msg)
		}
	}

	// --- Detalle de errores (en terminal Y en el log) ---
	// Los omitidos ya se mostraron y registraron antes de la confirmación
	// (también en dry-run); acá se consolidan los errores de la corrida.
	if len(erroresDetalle) > 0 {
		mostrar(log, "")
		mostrar(log, fmt.Sprintf("=== DETALLE DE ERRORES (%d) ===", len(erroresDetalle)))
		for _, e := range erroresDetalle {
			mostrar(log, e)
		}
	}

	// --- Resumen final ---
	fmt.Println()
	fmt.Println("==============================================")
	fmt.Println("  RESULTADOS")
	fmt.Println("==============================================")
	fmt.Printf("  Comandos ejecutados: %d\n", ejecutados)
	fmt.Printf("  Errores:             %d\n", errores)
	fmt.Printf("  Omitidos:            %d\n", len(omitidos))
	estado := "ÉXITO"
	if errores > 0 {
		estado = "CON ERRORES"
	}
	fmt.Printf("  Estado: %s\n", estado)
	fmt.Printf("  Log: %s\n", log.Path())
	fmt.Println("==============================================")

	log.Write("")
	log.Info(fmt.Sprintf("RESUMEN FINAL - ejecutados=%d errores=%d omitidos=%d estado=%s",
		ejecutados, errores, len(omitidos), estado))

	if errores > 0 {
		return 1
	}
	return 0
}

// mostrar imprime el mensaje en la terminal y además lo escribe en el log, para
// que el detalle (omitidos, errores) quede en ambos lados.
func mostrar(log *logger.Logger, msg string) {
	fmt.Println(msg)
	log.Write(msg)
}

// verificarOLTDestino comprueba que la OLT de destino (OLT_HOST) y la OLT
// de origen de datos (PROVISIONING_ID_OLT) estén presentes en la tabla `olt`.
// Si coinciden, lo indica. Si no coinciden, emite una advertencia pero permite
// continuar: en este flujo `PROVISIONING_ID_OLT` puede ser la OLT origen de datos
// y `OLT_HOST` la OLT destino donde se va a escribir.
func verificarOLTDestino(db *database.DB, cfg *config.Config) int {
	idOlt := cfg.Query.ProvisioningIDOlt
	host := strings.TrimSpace(cfg.OLT.Host)

	ipAdmin, found, err := db.GetOLTIPAdmin(context.Background(), idOlt)
	switch {
	case err != nil:
		fmt.Printf("ADVERTENCIA: no se pudo verificar el idOlt de provisioning en la tabla 'olt': %v\n", err)
		fmt.Printf("  Verificá a mano que OLT_HOST=%s sea la OLT destino y PROVISIONING_ID_OLT=%d la OLT origen de datos.\n\n", host, idOlt)
		return 0
	case !found:
		fmt.Printf("ADVERTENCIA: idOlt=%d no está en la tabla 'olt'; no se pudo verificar la OLT de origen de datos.\n", idOlt)
		fmt.Printf("  OLT_HOST sigue siendo la OLT destino: %s.\n\n", host)
		return 0
	case strings.TrimSpace(ipAdmin) != host:
		fmt.Println("ADVERTENCIA: OLT_HOST no coincide con la ipAdmin del idOlt configurado.")
		fmt.Printf("  PROVISIONING_ID_OLT=%d  ->  ipAdmin en tabla 'olt': %s\n", idOlt, strings.TrimSpace(ipAdmin))
		fmt.Printf("  OLT_HOST (.env):             %s\n", host)
		fmt.Println("  Se continuará porque PROVISIONING_ID_OLT puede ser la OLT origen de los datos y OLT_HOST la OLT destino.")
		fmt.Println()
		return 0
	default:
		fmt.Printf("OLT destino verificada: idOlt=%d -> ipAdmin %s coincide con OLT_HOST.\n\n", idOlt, host)
		return 0
	}
}

// ejecutarServicePorts asigna los índices en vivo y ejecuta un 'service-port'
// por cada pendiente. Devuelve (ejecutados, errores, asignaciones), donde
// asignaciones lleva el índice que quedó creado en la OLT junto con las filas de
// registro_cliente que cubre — solo de los comandos que salieron bien, para que
// sp_index refleje lo realmente configurado.
func ejecutarServicePorts(conn *olt.Conn, log *logger.Logger, pendientes []entrada, spInicio int) (ejecutados int, erroresDetalle []string, asignaciones []database.AsignacionSP) {
	// --- Asignación de índices de service-port (en vivo, sin colisión) ---
	fmt.Println("Consultando índices de service-port ocupados...")
	ocupados, err := conn.IndicesServicePortOcupados()
	if err != nil {
		msg := "consultando service-port ocupados: " + err.Error()
		fmt.Println("ERROR: " + msg)
		return 0, []string{msg}, nil
	}
	// Una ONT con varias VLANs necesita un service-port (y un índice) por VLAN.
	total := 0
	for _, e := range pendientes {
		total += len(e.sps)
	}
	indices := olt.SiguienteIndicesLibres(ocupados, total, spInicio)
	usados := 0
	for i := range pendientes {
		for j := range pendientes[i].sps {
			pendientes[i].sps[j].num = indices[usados]
			usados++
		}
	}
	msgIndices := fmt.Sprintf("%d índices ocupados detectados. Índices nuevos asignados: %v", len(ocupados), indices)
	fmt.Println(msgIndices)
	log.Info(msgIndices)

	fmt.Printf("\nEjecutando %d comandos service-port...\n", total)
	log.Write("")
	log.Write("=== EJECUCIÓN EN OLT (service-port) ===")

	hechos := 0
	for _, e := range pendientes {
		for _, sp := range e.sps {
			spNum := sp.num
			comando := fmt.Sprintf("service-port %d %s", spNum, sp.sufijo)
			hechos++
			fmt.Printf("[%d/%d] %s\n", hechos, total, comando)

			resp, err := conn.ExecuteCommand(comando)
			if err != nil {
				msg := fmt.Sprintf("[transporte] SP %d (%s): %v", spNum, comando, err)
				fmt.Println("  ERROR: " + msg)
				erroresDetalle = append(erroresDetalle, msg)
				// error de transporte es fatal: no tiene sentido seguir. Se
				// devuelve lo acumulado hasta acá para no perder el registro de
				// los service-port que sí quedaron creados.
				return ejecutados, erroresDetalle, asignaciones
			}

			// La OLT escupe notificaciones asincrónicas (logins a la web, alarmas)
			// que pueden pisar el eco del comando y comerse los primeros caracteres,
			// dejándolo como 'Bad command'. En ese caso el comando nunca llegó
			// entero, así que se reintenta una vez antes de darlo por fallado.
			if ecoCorrompido(resp, comando) {
				log.Info(fmt.Sprintf("SP %d: eco corrompido por mensaje asíncrono de la OLT, reintentando", spNum))
				resp, err = conn.ExecuteCommand(comando)
				if err != nil {
					msg := fmt.Sprintf("[transporte] SP %d (%s): %v", spNum, comando, err)
					fmt.Println("  ERROR: " + msg)
					erroresDetalle = append(erroresDetalle, msg)
					return ejecutados, erroresDetalle, asignaciones
				}
			}

			if detectaError(resp) {
				msg := fmt.Sprintf("SP %d (%s) -> %s", spNum, comando, strings.TrimSpace(resp))
				fmt.Println("  ADVERTENCIA: " + msg)
				erroresDetalle = append(erroresDetalle, msg)
			} else {
				fmt.Println("  OK")
				ejecutados++
				// Solo los que salieron bien se anotan para escribir sp_index.
				if len(sp.ids) > 0 {
					asignaciones = append(asignaciones, database.AsignacionSP{SPIndex: spNum, IDs: sp.ids})
				}
			}
		}
	}
	return ejecutados, erroresDetalle, asignaciones
}

// ecoCorrompido detecta el caso en que la OLT respondió 'Bad command' pero el
// comando enviado era válido: pasa cuando un mensaje asíncrono del firmware
// (ej. '#2025-03-03 01:10:39,[User]/5/Login the web by admin...') se intercala
// con el eco y se come los primeros caracteres, y la OLT termina leyendo
// 'ervice-port 4603 ...'. Se distingue de un comando realmente mal formado
// porque el eco no contiene la primera palabra de lo que mandamos.
func ecoCorrompido(resp, comando string) bool {
	if !strings.Contains(strings.ToLower(resp), "bad command") {
		return false
	}
	primera, _, _ := strings.Cut(comando, " ")
	return !strings.Contains(resp, primera)
}

// ejecutarRegistroOnu da de alta las ONTs con 'ont add'. Como 'ont add' solo es
// válido dentro de 'interface gpon 1/1/<puerto>', agrupa las pendientes por
// puerto: entra a la interfaz, ejecuta todas las de ese puerto y sale con
// 'exit'. Devuelve (ejecutados, errores).
func ejecutarRegistroOnu(conn *olt.Conn, log *logger.Logger, pendientes []entrada) (ejecutados int, erroresDetalle []string) {
	// Agrupar por puerto conservando el orden de aparición.
	var orden []int
	porPuerto := map[int][]entrada{}
	for _, e := range pendientes {
		p := e.reg.PuertoOlt
		if _, vista := porPuerto[p]; !vista {
			orden = append(orden, p)
		}
		porPuerto[p] = append(porPuerto[p], e)
	}

	fmt.Printf("\nRegistrando %d ONTs en %d puerto(s)...\n", len(pendientes), len(orden))
	log.Write("")
	log.Write("=== EJECUCIÓN EN OLT (ont add) ===")

	for _, puerto := range orden {
		entradas := porPuerto[puerto]
		cmdIf := fmt.Sprintf("interface gpon 1/1/%d", puerto)
		fmt.Printf("\n%s (%d ONTs)\n", cmdIf, len(entradas))
		if _, err := conn.ExecuteCommand(cmdIf); err != nil {
			msg := fmt.Sprintf("[transporte] entrando a %s: %v", cmdIf, err)
			fmt.Println("  ERROR: " + msg)
			erroresDetalle = append(erroresDetalle, msg)
			break // transporte fatal
		}

		fatal := false
		for i, e := range entradas {
			fmt.Printf("[%d/%d] %s\n", i+1, len(entradas), e.comandoOnt)
			resp, err := conn.ExecuteCommand(e.comandoOnt)
			if err != nil {
				msg := fmt.Sprintf("[transporte] ont add puerto %d (%s): %v", puerto, e.comandoOnt, err)
				fmt.Println("  ERROR: " + msg)
				erroresDetalle = append(erroresDetalle, msg)
				fatal = true
				break
			}
			if detectaError(resp) {
				msg := fmt.Sprintf("ont add puerto %d (%s) -> %s", puerto, e.comandoOnt, strings.TrimSpace(resp))
				fmt.Println("  ADVERTENCIA: " + msg)
				erroresDetalle = append(erroresDetalle, msg)
			} else {
				fmt.Println("  OK")
				ejecutados++
			}
		}

		// Salir de la interfaz gpon antes del próximo puerto (o de terminar).
		if _, err := conn.ExecuteCommand("exit"); err != nil {
			erroresDetalle = append(erroresDetalle, fmt.Sprintf("[transporte] saliendo de %s: %v", cmdIf, err))
		}
		if fatal {
			break
		}
	}
	return ejecutados, erroresDetalle
}

// seleccionarPendientes agrupa los registros listos para cargar por ONU y
// arma un solo par 'ont add' / 'service-port' por cada ONT. La consulta de
// provisioning ahora lee de registro_onu / registro_cliente, así que aquí ya
// se espera que los registros vengan filtrados por listo_para_cargar=1.
func seleccionarPendientes(registros []database.RegistroServicio) (pendientes []entrada, omitidos []string) {
	type key struct{ puerto, identificador int }
	type agrupado struct {
		reg database.RegistroServicio
		// vlans mapea cada VLAN de la ONT a las filas de registro_cliente que la
		// piden. Suele ser una sola, pero una misma ONU+VLAN puede tener varios
		// clientes (registro_cliente es único por id_olt+mac_wan+vlan): todos
		// quedan cubiertos por el mismo service-port y reciben su sp_index.
		vlans map[int][]int64
		gemId int
		tc    int
		desc  string
	}

	grupos := map[key]*agrupado{}
	var orden []key

	for _, r := range registros {
		// La fila ya debe venir lista para cargar; si no tiene SN o VLAN válida
		// no se puede armar el comando y se omite.
		if !r.Sn.Valid || strings.TrimSpace(r.Sn.String) == "" {
			omitidos = append(omitidos, omitido(r, "sn_vacio", "sn=(vacío)"))
			continue
		}
		if r.Vlan <= 0 {
			omitidos = append(omitidos, omitido(r, "vlan_invalida", fmt.Sprintf("vlan=%d", r.Vlan)))
			continue
		}

		ident := r.Identificador
		if r.OnuIdentificador.Valid {
			ident = int(r.OnuIdentificador.Int64)
		}
		k := key{puerto: r.PuertoOlt, identificador: ident}

		g, existe := grupos[k]
		if !existe {
			g = &agrupado{
				reg:   r,
				vlans: map[int][]int64{},
				gemId: 1,
				tc:    codigoTrafico(r),
				desc:  descripcion(r),
			}
			if r.GemId.Valid && r.GemId.Int64 > 0 {
				g.gemId = int(r.GemId.Int64)
			}
			orden = append(orden, k)
			grupos[k] = g
		}

		if r.Vlan > 0 {
			g.vlans[r.Vlan] = append(g.vlans[r.Vlan], r.ID)
		}
		if g.gemId <= 0 && r.GemId.Valid && r.GemId.Int64 > 0 {
			g.gemId = int(r.GemId.Int64)
		}
		if g.desc == "" {
			g.desc = descripcion(r)
		}
		// El código de tráfico es por ONU, pero sus MACs pueden traer planes
		// distintos. 0 (plan nulo) y 1 (default) son valores débiles: si otra MAC
		// de la misma ONU tiene un plan reconocido, ese código manda. Un plan
		// reconocido nunca se pisa con 0.
		if g.tc <= 1 {
			if tc := codigoTrafico(r); tc > 0 && tc != g.tc {
				g.tc = tc
			}
		}
	}

	for _, k := range orden {
		g := grupos[k]
		if len(g.vlans) == 0 {
			omitidos = append(omitidos, omitido(g.reg, "sin_vlans", "no hay vlans válidas"))
			continue
		}

		vlans := make([]int, 0, len(g.vlans))
		for v := range g.vlans {
			vlans = append(vlans, v)
		}
		sort.Ints(vlans)

		// La OLT acepta una sola VLAN por service-port: pasarle la lista
		// separada por comas ('svlan 3996,4000') devuelve 'Invalid parameter'.
		// Por eso se arma un comando por cada VLAN de la ONT.
		sps := make([]servicePort, 0, len(vlans))
		for _, v := range vlans {
			sps = append(sps, servicePort{
				vlan: v,
				ids:  g.vlans[v],
				sufijo: fmt.Sprintf(
					"config gpon 1/1/%d ont %d gem-id %d svlan %d user-vlan %d tag-action transparent%s%s",
					g.reg.PuertoOlt, k.identificador, g.gemId, v, v, sufijoTrafico(g.tc), sufijoDesc(g.desc),
				),
			})
		}
		comandoOnt := fmt.Sprintf(
			"ont add %d sn-auth %s ont-lineprofile-id 1 ont-srvprofile-id 1%s",
			k.identificador, formatearSN(strings.TrimSpace(g.reg.Sn.String)), sufijoDesc(g.desc),
		)

		pendientes = append(pendientes, entrada{reg: g.reg, sps: sps, comandoOnt: comandoOnt, tc: g.tc})
	}
	return pendientes, omitidos
}

// sufijoTrafico devuelve " traffic-in <tc> traffic-out <tc>", o cadena vacía si
// no hay plan (tc = 0). Mismo criterio que sufijoDesc: el parámetro que no tiene
// dato NO se manda, porque mandarlo vacío hace que la OLT rechace el comando
// entero. Es el caso de las posiciones 'plan no reconocido': el service-port se
// crea sin perfil de tráfico y el plan se carga a mano en el equipo.
func sufijoTrafico(tc int) string {
	if tc <= 0 {
		return ""
	}
	return fmt.Sprintf(" traffic-in %d traffic-out %d", tc, tc)
}

// sufijoDesc devuelve " desc <valor>" listo para concatenar, o cadena vacía si
// no hay descripción: un 'desc' sin valor hace que la OLT rechace el comando
// entero con 'Missing parameter data'.
func sufijoDesc(desc string) string {
	desc = strings.TrimSpace(desc)
	if desc == "" {
		return ""
	}
	return " desc " + desc
}

func clienteTexto(r database.RegistroServicio) string {
	if r.Cliente.Valid {
		return strings.TrimSpace(r.Cliente.String)
	}
	return ""
}

// omitido arma una línea uniforme para un equipo que no se cargó, identificándolo
// (MAC, cliente, puerto, ONU-ID) y explicando por qué (motivo + detalle), para
// que en el log se vea claro qué pasó con cada uno.
func omitido(r database.RegistroServicio, motivo, detalle string) string {
	return fmt.Sprintf("MAC=%-17s cliente=%-24s puerto=%-2d ident=%-3d motivo=%s %s",
		r.Mac, clienteTexto(r), r.PuertoOlt, r.Identificador, motivo, detalle)
}

// estadosCargables son los estadoPresencia de onu que se procesan. El resto
// (ausente, sin_trafico, sin_identificar) se ignora. Los valores provienen de
// olt-nacho-git/read-olt (internal/repository/onu_repository.go).
var estadosCargables = map[string]bool{
	"mac_verificada":    true,
	"mac_sin_verificar": true,
	"macs_multiples":    true,
}

// estadoPresenciaCargable indica si la ONU del registro está en un estado que
// habilita su carga (registro + service-port).
func estadoPresenciaCargable(r database.RegistroServicio) bool {
	if !r.EstadoPresencia.Valid {
		return false
	}
	return estadosCargables[strings.ToLower(strings.TrimSpace(r.EstadoPresencia.String))]
}

// descripcion devuelve el texto de 'desc': el nroConexion del cliente si existe,
// si no el nombre de cliente (pppoe). Se usa igual en el 'ont add' y en el
// 'service-port'.
func descripcion(r database.RegistroServicio) string {
	desc := r.NroConexion.String
	if !r.NroConexion.Valid || desc == "" {
		desc = clienteTexto(r)
	}
	return desc
}

// formatearSN inserta un guión después del 4º carácter del serial, como espera
// la OLT en 'sn-auth' (ej. TPLGF58E9CB8 -> TPLG-F58E9CB8).
func formatearSN(sn string) string {
	sn = strings.TrimSpace(sn)
	if len(sn) <= 4 {
		return sn
	}
	return sn[:4] + "-" + sn[4:]
}

// codigoTrafico convierte el plan del cliente en el código de traffic-in/out.
//
// Devuelve 0 —plan NULL, sin traffic-in/out— para las filas marcadas como 'plan
// no reconocido': ese plan no existe en la tabla `planes` y se carga a mano en
// el equipo, así que el service-port se crea sin perfil de tráfico. El resto de
// los planes que no matchean caen en el valor por defecto (1).
func codigoTrafico(r database.RegistroServicio) int {
	if r.EstadoRegistro.Valid && database.EsPlanNoReconocido(r.EstadoRegistro.String) {
		return 0
	}
	if !r.Plan.Valid {
		return 1
	}
	plan := strings.TrimSpace(r.Plan.String)
	switch plan {
	case "100 MB Hogar":
		return 1
	case "200 MB Hogar":
		return 2
	case "300 MB Hogar":
		return 3
	case "100 MB Pyme":
		return 4
	case "200 MB Pyme":
		return 5
	case "300 MB Pyme":
		return 6
	}

	switch velocidadPlan(plan) {
	case 2, 6, 8, 10:
		return 7
	}
	return 1
}

// velocidadPlan extrae la velocidad en MB del comienzo del nombre del plan,
// p. ej. "8 MB Hogar" -> 8. Devuelve 0 si no hay un número al inicio.
func velocidadPlan(plan string) int {
	i := 0
	for i < len(plan) && plan[i] >= '0' && plan[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0
	}
	n, _ := strconv.Atoi(plan[:i])
	return n
}

// detectaError busca palabras clave de error en la respuesta de la OLT.
func detectaError(resp string) bool {
	low := strings.ToLower(resp)
	for _, p := range []string{"error", "failure", "failed", "invalid", "not found", "already exists", "conflict"} {
		if strings.Contains(low, p) {
			return true
		}
	}
	return false
}

// spInicioDesdeEnv lee PROVISIONING_SP_INICIO del entorno (default 3699).
func spInicioDesdeEnv() int {
	if v := os.Getenv("PROVISIONING_SP_INICIO"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return 3699
}
