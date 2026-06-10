// Package registrar orquesta el proceso de alta de clientes en la OLT. Es el
// equivalente de src/OltClientRegistrar.php. Depende de la OLT a través de una
// interfaz (no del tipo concreto) para poder testearlo con un mock.
package registrar

import (
	"fmt"
	"strings"

	"oltssh/internal/database"
)

// OLT es la porción de la conexión que necesita el registrador. La satisface
// *olt.Conn, y en tests puede sustituirse por un mock.
type OLT interface {
	Connect() error
	EnableMode() error
	ExecuteCommand(cmd string) (string, error)
	SaveConfig() error
	Disconnect() error
}

// Logger es la parte del logger de sesión que necesita el registrador para dejar
// constancia en el archivo de log de los errores de conexión y excepciones, igual
// que la conexión registra cada comando ejecutado. La satisface *logger.Logger y,
// en tests, puede sustituirse por un mock (o ser nil para no registrar nada).
type Logger interface {
	Error(message string)
}

// Detalle es la traza de un comando ejecutado y su respuesta.
type Detalle struct {
	Tipo      string // "ont_add" | "service_port"
	Comando   string
	Respuesta string
}

// ErrItem describe un error detectado. Si proviene de una respuesta de la OLT
// lleva Comando+Respuesta; si proviene de una excepción lleva Mensaje.
type ErrItem struct {
	Tipo      string
	Comando   string
	Respuesta string
	Mensaje   string
}

// Result es el resultado del proceso completo.
type Result struct {
	Success            bool
	ComandosEjecutados int
	Errores            []ErrItem
	Detalles           []Detalle
}

// Registrar ejecuta el alta de un conjunto de comandos sobre un puerto GPON.
type Registrar struct {
	olt       OLT
	logger    Logger // opcional: si es nil no se registra nada en el archivo de log
	comandos  []database.Comando
	puertoOlt int
}

// New crea el registrador con la conexión, el logger de sesión (puede ser nil),
// los comandos ya obtenidos de la BD y el puerto GPON destino.
func New(o OLT, log Logger, comandos []database.Comando, puertoOlt int) *Registrar {
	return &Registrar{olt: o, logger: log, comandos: comandos, puertoOlt: puertoOlt}
}

// palabras que delatan un error en la respuesta de la OLT.
var errorPatterns = []string{
	"error", "failure", "failed", "invalid", "not found", "already exists", "conflict",
}

// Run ejecuta el proceso completo y devuelve el resultado. No retorna error: los
// fallos (de transporte o de comando) se acumulan en Result.Errores, igual que el
// try/catch del PHP. La desconexión queda garantizada con defer.
func (r *Registrar) Run() Result {
	res := Result{}

	// Garantiza el cierre de la conexión pase lo que pase (equivale al finally).
	defer r.olt.Disconnect()

	// Sin comandos no hay nada que hacer (no es error).
	if len(r.comandos) == 0 {
		r.log("No se encontraron ONTs para registrar")
		res.Success = true
		return res
	}

	r.log(fmt.Sprintf("Se encontraron %d ONTs para registrar", len(r.comandos)))

	// 1. Conexión SSH.
	r.log("Conectando a la OLT...")
	if err := r.olt.Connect(); err != nil {
		r.addException(&res, err)
		return res
	}

	// 2. Modo privilegiado.
	r.log("Entrando en modo enable...")
	if err := r.olt.EnableMode(); err != nil {
		r.addException(&res, err)
		return res
	}

	// 3. Modo configuración global.
	r.log("Entrando en modo configuración...")
	if _, err := r.olt.ExecuteCommand("configure"); err != nil {
		r.addException(&res, err)
		return res
	}

	// 4. Interfaz GPON del puerto configurado.
	r.log(fmt.Sprintf("Entrando en interface gpon 1/1/%d...", r.puertoOlt))
	if _, err := r.olt.ExecuteCommand(fmt.Sprintf("interface gpon 1/1/%d", r.puertoOlt)); err != nil {
		r.addException(&res, err)
		return res
	}

	// 5. PASADA 1: da de alta TODAS las ONTs (comando2 = 'ont add') primero.
	r.log("Ejecutando comandos de registro de ONT (comando2)...")
	for i, c := range r.comandos {
		r.log(fmt.Sprintf("[%d/%d] %s", i+1, len(r.comandos), c.Comando2))
		if err := r.execTracked(&res, "ont_add", c.Comando2); err != nil {
			r.addException(&res, err)
			return res
		}
	}

	// 6. Salir de la interfaz gpon para volver a config.
	r.log("Saliendo de interface gpon...")
	if _, err := r.olt.ExecuteCommand("exit"); err != nil {
		r.addException(&res, err)
		return res
	}

	// 7. PASADA 2: asocia VLAN/servicio a cada ONT (comando1 = 'service-port').
	r.log("Ejecutando comandos de service-port (comando1)...")
	for i, c := range r.comandos {
		r.log(fmt.Sprintf("[%d/%d] %s", i+1, len(r.comandos), c.Comando1))
		if err := r.execTracked(&res, "service_port", c.Comando1); err != nil {
			r.addException(&res, err)
			return res
		}
	}

	// 8. Persistir la configuración.
	r.log("Guardando configuración...")
	if err := r.olt.SaveConfig(); err != nil {
		r.addException(&res, err)
		return res
	}

	// Éxito solo si no se acumuló ningún error.
	res.Success = len(res.Errores) == 0
	r.log(fmt.Sprintf("Proceso completado. Comandos ejecutados: %d, Errores: %d",
		res.ComandosEjecutados, len(res.Errores)))

	return res
}

// execTracked ejecuta un comando, registra el detalle y, si la respuesta delata
// un error, lo acumula. Devuelve error solo ante fallos de transporte (fatales).
func (r *Registrar) execTracked(res *Result, tipo, comando string) error {
	resp, err := r.olt.ExecuteCommand(comando)
	if err != nil {
		return err
	}
	res.Detalles = append(res.Detalles, Detalle{Tipo: tipo, Comando: comando, Respuesta: resp})
	res.ComandosEjecutados++
	if hasError(resp) {
		res.Errores = append(res.Errores, ErrItem{Comando: comando, Respuesta: resp})
		// Dejamos constancia del error detectado en el archivo de log, además del
		// comando/respuesta que ya registró la conexión.
		if r.logger != nil {
			r.logger.Error("error detectado en la respuesta del comando: " + comando + " -> " + strings.TrimSpace(resp))
		}
	}
	return nil
}

// addException registra una excepción (fallo fatal: error de conexión o cualquier
// otro fallo de transporte) en el resultado y, si hay logger, también en el log.
func (r *Registrar) addException(res *Result, err error) {
	res.Errores = append(res.Errores, ErrItem{Tipo: "exception", Mensaje: err.Error()})
	if r.logger != nil {
		r.logger.Error("excepción durante el registro: " + err.Error())
	}
}

// hasError aplica la heurística de palabras clave para detectar fallos.
func hasError(response string) bool {
	low := strings.ToLower(response)
	for _, p := range errorPatterns {
		if strings.Contains(low, p) {
			return true
		}
	}
	return false
}

// log hace eco de progreso por pantalla.
func (r *Registrar) log(message string) {
	fmt.Println(message)
}
