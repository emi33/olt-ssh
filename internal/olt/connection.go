// Package olt maneja la conexión SSH a la OLT y la ejecución de comandos sobre su
// consola interactiva (shell con PTY). Es el equivalente de src/OltConnection.php,
// usando golang.org/x/crypto/ssh en lugar de phpseclib3.
package olt

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"oltssh/internal/config"
	"oltssh/internal/logger"

	"golang.org/x/crypto/ssh"
)

// Expresiones regulares reutilizadas (compiladas una sola vez).
var (
	// Prompt de la OLT: termina en '>' o '#' (con posibles espacios al final).
	promptRe = regexp.MustCompile(`[>#]\s*$`)
	// Tras enviar 'en': o pide contraseña o ya muestra el prompt privilegiado.
	enableRe = regexp.MustCompile(`[Pp]assword:|#`)
	// Prompt privilegiado a secas.
	hashRe = regexp.MustCompile(`#`)

	// Limpieza de secuencias de escape ANSI (colores y movimientos de cursor).
	ansiRe1 = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
	ansiRe2 = regexp.MustCompile(`\x1b\[\d+D`)
)

// Conn representa una sesión SSH interactiva con la OLT.
type Conn struct {
	cfg    config.OLTConfig
	logger *logger.Logger

	client  *ssh.Client
	session *ssh.Session
	stdin   io.WriteCloser

	// readCh recibe los fragmentos leídos del stdout de la sesión.
	readCh chan []byte

	inEnable bool
}

// New crea la conexión (todavía sin conectar). El logger es opcional.
func New(cfg config.OLTConfig, log *logger.Logger) *Conn {
	return &Conn{cfg: cfg, logger: log}
}

// Connect abre la sesión SSH con la OLT. Reintenta hasta cfg.ConnectAttempts
// veces (esperando cfg.RetryDelay entre intentos), limpiando el estado parcial
// entre uno y otro. Si se agotan los intentos, devuelve el mensaje del último
// error para que el llamador lo muestre.
func (c *Conn) Connect() error {
	attempts := c.cfg.ConnectAttempts
	if attempts < 1 {
		attempts = 1
	}

	var lastErr error
	for intento := 1; intento <= attempts; intento++ {
		if err := c.connectOnce(); err == nil {
			if intento > 1 {
				c.log(fmt.Sprintf("Conexión a la OLT establecida en el intento %d/%d.", intento, attempts))
			}
			return nil
		} else {
			lastErr = err
			// Cerramos lo que se hubiera abierto a medias para que el siguiente
			// intento parta de un estado limpio.
			c.cleanup()
			c.log(fmt.Sprintf("Intento %d/%d de conexión a la OLT fallido: %v", intento, attempts, err))
			if intento < attempts {
				c.log(fmt.Sprintf("Reintentando en %s...", c.cfg.RetryDelay))
				time.Sleep(c.cfg.RetryDelay)
			}
		}
	}

	return fmt.Errorf("error de conexión/autenticación SSH en la OLT tras %d intento(s): %w", attempts, lastErr)
}

// cleanup cierra la sesión y el cliente SSH abiertos a medias y descarta el estado
// para que un reintento de Connect arranque desde cero.
func (c *Conn) cleanup() {
	if c.session != nil {
		c.session.Close()
		c.session = nil
	}
	if c.client != nil {
		c.client.Close()
		c.client = nil
	}
	c.stdin = nil
	// La goroutine lectora conserva su propia referencia al canal y termina sola
	// cuando el stdout se cierra al cerrar la sesión/cliente.
	c.readCh = nil
}

// connectOnce realiza un único intento de conexión: fuerza los algoritmos legacy
// que usan muchas OLTs antiguas, pide un PTY, arranca el shell y consume el prompt
// inicial.
func (c *Conn) connectOnce() error {
	sshCfg := &ssh.ClientConfig{
		User:            c.cfg.Username,
		Auth:            []ssh.AuthMethod{ssh.Password(c.cfg.Password)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // el PHP tampoco verificaba host key
		Timeout:         c.cfg.Timeout,
		// Algoritmos heredados equivalentes a los del setPreferredAlgorithms() del PHP.
		Config: ssh.Config{
			KeyExchanges: []string{"diffie-hellman-group1-sha1", "diffie-hellman-group14-sha1"},
			Ciphers:      []string{"aes128-cbc", "aes256-cbc", "3des-cbc"},
		},
		HostKeyAlgorithms: []string{"ssh-rsa", "ssh-dss"},
	}

	addr := fmt.Sprintf("%s:%d", c.cfg.Host, c.cfg.Port)
	client, err := ssh.Dial("tcp", addr, sshCfg)
	if err != nil {
		return fmt.Errorf("estableciendo conexión SSH: %w", err)
	}
	c.client = client

	session, err := client.NewSession()
	if err != nil {
		client.Close()
		return fmt.Errorf("abriendo sesión SSH: %w", err)
	}
	c.session = session

	// Pseudo-terminal: la OLT necesita un shell interactivo.
	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	if err := session.RequestPty("vt100", 80, 40, modes); err != nil {
		return fmt.Errorf("solicitando PTY: %w", err)
	}

	stdin, err := session.StdinPipe()
	if err != nil {
		return fmt.Errorf("abriendo stdin: %w", err)
	}
	c.stdin = stdin

	stdout, err := session.StdoutPipe()
	if err != nil {
		return fmt.Errorf("abriendo stdout: %w", err)
	}

	// Goroutine lectora: empuja todo lo que llega del stdout al canal readCh.
	c.startReader(stdout)

	if err := session.Shell(); err != nil {
		return fmt.Errorf("iniciando shell: %w", err)
	}

	c.log("Conectado a la OLT: " + c.cfg.Host)

	// Consumimos el prompt inicial.
	c.readUntil(promptRe, c.cfg.Timeout)
	return nil
}

// startReader lanza la goroutine que lee el stdout de la sesión en fragmentos.
func (c *Conn) startReader(stdout io.Reader) {
	c.readCh = make(chan []byte, 32)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := stdout.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				c.readCh <- chunk
			}
			if err != nil {
				close(c.readCh)
				return
			}
		}
	}()
}

// readUntil acumula la salida hasta que el regex casa o vence el timeout.
// Al igual que phpseclib en modo READ_REGEX, si vence el timeout devuelve lo que
// haya leído hasta el momento (sin error), para no abortar el flujo.
func (c *Conn) readUntil(re *regexp.Regexp, timeout time.Duration) string {
	var buf bytes.Buffer
	deadline := time.After(timeout)
	for {
		select {
		case chunk, ok := <-c.readCh:
			if !ok {
				return buf.String() // conexión cerrada: devolvemos lo acumulado
			}
			buf.Write(chunk)
			if re.Match(buf.Bytes()) {
				return buf.String()
			}
		case <-deadline:
			return buf.String()
		}
	}
}

// EnableMode entra en modo privilegiado. Es idempotente.
func (c *Conn) EnableMode() error {
	if c.inEnable {
		return nil
	}

	if _, err := c.stdin.Write([]byte("en\r")); err != nil {
		return fmt.Errorf("enviando 'en': %w", err)
	}
	time.Sleep(100 * time.Millisecond)
	resp := c.readUntil(enableRe, c.cfg.Timeout)

	// Si la OLT pide contraseña de enable, la enviamos.
	if strings.Contains(strings.ToLower(resp), "password") {
		if _, err := c.stdin.Write([]byte(c.cfg.EnablePassword + "\r")); err != nil {
			return fmt.Errorf("enviando contraseña de enable: %w", err)
		}
		time.Sleep(100 * time.Millisecond)
		c.readUntil(hashRe, c.cfg.Timeout)
	}

	c.inEnable = true
	c.log("Modo enable activado")
	return nil
}

// ExecuteCommand envía un comando y devuelve la respuesta ya limpia de ANSI,
// con el timeout de lectura por defecto (10s).
func (c *Conn) ExecuteCommand(command string) (string, error) {
	return c.executeCommand(command, 10*time.Second)
}

// executeCommand es la implementación con timeout configurable.
func (c *Conn) executeCommand(command string, timeout time.Duration) (string, error) {
	if c.stdin == nil {
		return "", errors.New("la conexión no está establecida")
	}

	// Envía el comando seguido de retorno de carro (la OLT espera \r).
	if _, err := c.stdin.Write([]byte(command + "\r")); err != nil {
		return "", fmt.Errorf("escribiendo comando %q: %w", command, err)
	}

	// Pequeña pausa para que la OLT procese antes de leer.
	time.Sleep(100 * time.Millisecond)

	// Lee hasta el prompt y limpia las secuencias de escape.
	raw := c.readUntil(promptRe, timeout)
	clean := cleanANSI(raw)

	c.log("Comando: " + command)
	c.log("Respuesta: " + strings.TrimSpace(clean))

	if c.logger != nil {
		c.logger.Command(command, clean)
	}

	return clean, nil
}

// SaveConfig persiste la configuración en la OLT ('save'), confirmando con 'y'.
func (c *Conn) SaveConfig() error {
	// 'save' puede tardar; ampliamos el timeout a 30s.
	if _, err := c.executeCommand("save", 30*time.Second); err != nil {
		return err
	}
	// Algunas OLTs piden confirmación (y/n): respondemos 'y'.
	time.Sleep(500 * time.Millisecond)
	if _, err := c.stdin.Write([]byte("y\r")); err != nil {
		return fmt.Errorf("confirmando guardado: %w", err)
	}
	time.Sleep(100 * time.Millisecond)
	c.readUntil(hashRe, c.cfg.Timeout)
	c.log("Configuración guardada")
	return nil
}

// Disconnect cierra la sesión y la conexión SSH.
func (c *Conn) Disconnect() error {
	if c.session != nil {
		c.session.Close()
		c.session = nil
	}
	if c.client != nil {
		err := c.client.Close()
		c.client = nil
		c.log("Desconectado de la OLT")
		return err
	}
	return nil
}

// log hace eco de estado por pantalla (el log a archivo lo hace el Logger).
func (c *Conn) log(message string) {
	fmt.Println(message)
}

// cleanANSI elimina las secuencias de escape ANSI de una respuesta.
func cleanANSI(s string) string {
	s = ansiRe1.ReplaceAllString(s, "")
	s = ansiRe2.ReplaceAllString(s, "")
	return s
}
