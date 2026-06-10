package registrar

import (
	"errors"
	"strings"
	"testing"

	"oltssh/internal/database"
)

// mockOLT implementa la interfaz OLT registrando el orden de los comandos y
// permitiendo inyectar respuestas/errores.
type mockOLT struct {
	commands  []string // orden en que se recibieron los comandos
	responses map[string]string
	connErr   error
}

// mockLogger implementa la interfaz Logger registrando los mensajes de error para
// poder verificarlos en los tests.
type mockLogger struct {
	errores []string
}

func (l *mockLogger) Error(message string) { l.errores = append(l.errores, message) }

func (m *mockOLT) Connect() error    { return m.connErr }
func (m *mockOLT) EnableMode() error { return nil }
func (m *mockOLT) SaveConfig() error { return nil }
func (m *mockOLT) Disconnect() error { return nil }

func (m *mockOLT) ExecuteCommand(cmd string) (string, error) {
	m.commands = append(m.commands, cmd)
	if r, ok := m.responses[cmd]; ok {
		return r, nil
	}
	return "OK", nil
}

func comandosEjemplo() []database.Comando {
	return []database.Comando{
		{Comando1: "service-port 3698 ...", Comando2: "ont add 1 ..."},
		{Comando1: "service-port 3699 ...", Comando2: "ont add 2 ..."},
	}
}

// TestRunOrdenDosPasadas verifica que primero se ejecutan todos los 'ont add'
// (dentro de interface gpon) y después todos los 'service-port', con el 'exit'
// en medio, replicando el flujo del PHP.
func TestRunOrdenDosPasadas(t *testing.T) {
	m := &mockOLT{}
	r := New(m, nil, comandosEjemplo(), 16)

	res := r.Run()

	if !res.Success {
		t.Fatalf("esperaba success=true, errores=%v", res.Errores)
	}
	if res.ComandosEjecutados != 4 {
		t.Fatalf("esperaba 4 comandos ejecutados, obtuve %d", res.ComandosEjecutados)
	}

	want := []string{
		"configure",
		"interface gpon 1/1/16",
		"ont add 1 ...",
		"ont add 2 ...",
		"exit",
		"service-port 3698 ...",
		"service-port 3699 ...",
	}
	if len(m.commands) != len(want) {
		t.Fatalf("esperaba %d comandos, obtuve %d: %v", len(want), len(m.commands), m.commands)
	}
	for i, c := range want {
		if m.commands[i] != c {
			t.Errorf("comando[%d]: esperaba %q, obtuve %q", i, c, m.commands[i])
		}
	}
}

// TestRunDetectaErrorEnRespuesta verifica que una respuesta con palabra de error
// se acumula y marca el resultado como no exitoso, sin detener el proceso.
func TestRunDetectaErrorEnRespuesta(t *testing.T) {
	m := &mockOLT{
		responses: map[string]string{
			"ont add 2 ...": "Failure: already exists",
		},
	}
	r := New(m, nil, comandosEjemplo(), 16)

	res := r.Run()

	if res.Success {
		t.Fatal("esperaba success=false por la respuesta de error")
	}
	if len(res.Errores) != 1 {
		t.Fatalf("esperaba 1 error, obtuve %d: %v", len(res.Errores), res.Errores)
	}
	if res.ComandosEjecutados != 4 {
		t.Errorf("el proceso debía continuar: esperaba 4 ejecutados, obtuve %d", res.ComandosEjecutados)
	}
}

// TestRunSinComandos verifica que sin comandos el resultado es exitoso y no se
// intenta conectar.
func TestRunSinComandos(t *testing.T) {
	m := &mockOLT{}
	r := New(m, nil, nil, 16)

	res := r.Run()

	if !res.Success {
		t.Fatal("esperaba success=true sin comandos")
	}
	if len(m.commands) != 0 {
		t.Errorf("no debía ejecutarse ningún comando, obtuve %v", m.commands)
	}
}

// TestRunErrorDeConexion verifica que un fallo de conexión se reporta como
// excepción y aborta el proceso.
func TestRunErrorDeConexion(t *testing.T) {
	m := &mockOLT{connErr: errors.New("conexión rechazada")}
	r := New(m, nil, comandosEjemplo(), 16)

	res := r.Run()

	if res.Success {
		t.Fatal("esperaba success=false por error de conexión")
	}
	if len(res.Errores) != 1 || res.Errores[0].Tipo != "exception" {
		t.Fatalf("esperaba un error de tipo exception, obtuve %v", res.Errores)
	}
}

// TestRunRegistraExcepcionEnLog verifica que un error de conexión (excepción) se
// registra en el archivo de log a través del logger, igual que los comandos.
func TestRunRegistraExcepcionEnLog(t *testing.T) {
	log := &mockLogger{}
	m := &mockOLT{connErr: errors.New("conexión rechazada")}
	r := New(m, log, comandosEjemplo(), 16)

	res := r.Run()

	if res.Success {
		t.Fatal("esperaba success=false por error de conexión")
	}
	if len(log.errores) != 1 {
		t.Fatalf("esperaba 1 error registrado en el log, obtuve %d: %v", len(log.errores), log.errores)
	}
	if !strings.Contains(log.errores[0], "conexión rechazada") {
		t.Errorf("el log no contiene el mensaje de la excepción: %q", log.errores[0])
	}
}

// TestRunRegistraErrorDeRespuestaEnLog verifica que un error detectado en la
// respuesta de un comando también se deja registrado en el log.
func TestRunRegistraErrorDeRespuestaEnLog(t *testing.T) {
	log := &mockLogger{}
	m := &mockOLT{
		responses: map[string]string{
			"ont add 2 ...": "Failure: already exists",
		},
	}
	r := New(m, log, comandosEjemplo(), 16)

	res := r.Run()

	if res.Success {
		t.Fatal("esperaba success=false por la respuesta de error")
	}
	if len(log.errores) != 1 {
		t.Fatalf("esperaba 1 error registrado en el log, obtuve %d: %v", len(log.errores), log.errores)
	}
	if !strings.Contains(log.errores[0], "ont add 2 ...") {
		t.Errorf("el log no identifica el comando con error: %q", log.errores[0])
	}
}

// TestHasError comprueba la heurística de detección de errores.
func TestHasError(t *testing.T) {
	cases := map[string]bool{
		"command successful":      false,
		"ONT added":               false,
		"Error: invalid sn":       true,
		"FAILED to apply":         true,
		"already exists":          true,
		"Conflict detected":       true,
		"ont 1 not found in port": true,
	}
	for resp, want := range cases {
		if got := hasError(resp); got != want {
			t.Errorf("hasError(%q) = %v, esperaba %v", resp, got, want)
		}
	}
}
