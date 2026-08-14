// Package olt maneja la conexión a la OLT mediante sshpass + ssh externo.
// Se usa sshpass porque la OLT 192.168.25.1 presenta una clave DSA-512 que
// golang.org/x/crypto/ssh rechaza (tamaño inferior al mínimo de 1024 bits).
// El comportamiento externo (Connect, EnableMode, ExecuteCommand, etc.) es
// idéntico a la versión con la librería nativa.
package olt

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"oltssh/internal/config"
	"oltssh/internal/logger"
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

// Conn representa una sesión interactiva con la OLT via sshpass.
type Conn struct {
	cfg    config.OLTConfig
	logger *logger.Logger

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	readCh chan []byte

	inEnable bool
}

// New crea la conexión (todavía sin conectar). El logger es opcional.
func New(cfg config.OLTConfig, log *logger.Logger) *Conn {
	return &Conn{cfg: cfg, logger: log}
}

// Connect abre la sesión SSH con la OLT vía sshpass. Reintenta hasta
// cfg.ConnectAttempts veces (esperando cfg.RetryDelay entre intentos).
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
			c.cleanup()
			c.log(fmt.Sprintf("Intento %d/%d de conexión a la OLT fallido: %v", intento, attempts, err))
			if intento < attempts {
				c.log(fmt.Sprintf("Reintentando en %s...", c.cfg.RetryDelay))
				time.Sleep(c.cfg.RetryDelay)
			}
		}
	}

	return fmt.Errorf("error de conexión SSH a la OLT tras %d intento(s): %w", attempts, lastErr)
}

// connectOnce realiza un único intento de conexión usando sshpass + ssh externo.
// Los parámetros SSH cubren todos los algoritmos legacy que requiere la OLT.
func (c *Conn) connectOnce() error {
	timeout := int(c.cfg.Timeout.Seconds())
	if timeout < 1 {
		timeout = 30
	}

	args := []string{
		"-p", c.cfg.Password,
		"ssh",
		"-tt", // fuerza PTY en el servidor (necesario para sesión interactiva)
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "KexAlgorithms=+diffie-hellman-group1-sha1",
		"-o", "HostKeyAlgorithms=+ssh-dss",
		"-o", "PubkeyAcceptedAlgorithms=+ssh-dss",
		"-o", "Ciphers=+aes256-cbc,aes192-cbc,aes128-cbc",
		"-o", fmt.Sprintf("ConnectTimeout=%d", timeout),
		"-p", fmt.Sprintf("%d", c.cfg.Port),
		fmt.Sprintf("%s@%s", c.cfg.Username, c.cfg.Host),
	}

	c.cmd = exec.Command("sshpass", args...)

	stdin, err := c.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("abriendo stdin: %w", err)
	}
	c.stdin = stdin

	stdout, err := c.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("abriendo stdout: %w", err)
	}

	// stderr a un buffer para incluirlo en el mensaje de error si la conexión falla.
	var stderrBuf bytes.Buffer
	c.cmd.Stderr = &stderrBuf

	c.startReader(stdout)

	if err := c.cmd.Start(); err != nil {
		return fmt.Errorf("iniciando sshpass: %w", err)
	}

	c.log("Conectando a la OLT: " + c.cfg.Host)

	// Esperar el prompt inicial de la OLT.
	initial := c.readUntil(promptRe, c.cfg.Timeout)
	if !promptRe.MatchString(initial) {
		stderr := strings.TrimSpace(stderrBuf.String())
		if stderr != "" {
			return fmt.Errorf("conexión fallida: %s", stderr)
		}
		return fmt.Errorf("timeout esperando prompt inicial de la OLT (verificar credenciales y conectividad)")
	}

	c.log("Conectado a la OLT: " + c.cfg.Host)
	return nil
}

// cleanup cierra stdin y mata el proceso sshpass para que un reintento parta limpio.
func (c *Conn) cleanup() {
	if c.stdin != nil {
		c.stdin.Close()
		c.stdin = nil
	}
	if c.cmd != nil && c.cmd.Process != nil {
		c.cmd.Process.Kill()
		c.cmd.Wait()
		c.cmd = nil
	}
	c.readCh = nil
}

// startReader lanza la goroutine que lee el stdout de sshpass en fragmentos.
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

// drain descarta de forma no bloqueante lo que haya quedado en el canal de
// lectura (típicamente el eco/prompt ya consumido del comando anterior), para
// que la próxima lectura no matchee salida vieja. Es seguro porque solo se
// llama justo antes de escribir un comando nuevo: todo lo pendiente pertenece
// al comando anterior, cuyo prompt readUntil ya consumió.
func (c *Conn) drain() {
	for {
		select {
		case _, ok := <-c.readCh:
			if !ok {
				return
			}
		default:
			return
		}
	}
}

// readUntil acumula la salida hasta que el regex casa o vence el timeout.
func (c *Conn) readUntil(re *regexp.Regexp, timeout time.Duration) string {
	var buf bytes.Buffer
	deadline := time.After(timeout)
	for {
		select {
		case chunk, ok := <-c.readCh:
			if !ok {
				return buf.String()
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

// ExecuteCommand envía un comando y devuelve la respuesta ya limpia de ANSI.
// Usa el timeout por comando configurable (OLT_COMMAND_TIMEOUT, default 5s).
// readUntil devuelve apenas reaparece el prompt, así que este timeout solo es
// un techo para comandos que no responden; bajarlo acelera la detección de
// esos casos sin afectar el camino feliz.
func (c *Conn) ExecuteCommand(command string) (string, error) {
	timeout := c.cfg.CommandTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return c.executeCommand(command, timeout)
}

// executeCommand es la implementación con timeout configurable.
func (c *Conn) executeCommand(command string, timeout time.Duration) (string, error) {
	if c.stdin == nil {
		return "", errors.New("la conexión no está establecida")
	}

	// Descarta cualquier resto de salida del comando anterior (lo que haya
	// quedado en el canal después del prompt ya consumido) para no arrancar la
	// lectura con datos viejos. Reemplaza al antiguo 'sleep' fijo de 100ms por
	// comando: readUntil ya se bloquea esperando el eco del OLT, así que el
	// sleep solo sumaba latencia (~100ms x cada comando, ~1,7min sobre 1000+).
	c.drain()

	if _, err := c.stdin.Write([]byte(command + "\r")); err != nil {
		return "", fmt.Errorf("escribiendo comando %q: %w", command, err)
	}

	raw := c.readUntil(promptRe, timeout)
	clean := cleanANSI(raw)

	c.log("Comando: " + command)
	c.log("Respuesta: " + strings.TrimSpace(clean))

	if c.logger != nil {
		c.logger.Command(command, clean)
	}

	return clean, nil
}

// SaveConfig persiste la configuración en la OLT.
//
// Se asume que la sesión está en modo configuración global ('(config)#')
// al momento de llamar — así terminan registrar.Run() y cmd/provisionar
// después de la pasada de service-port — así que primero sale a modo
// privilegiado con 'end'. 'copy running-config startup-config' (igual que
// 'save') falla con "Error: Bad command" si se ejecuta desde '(config)#';
// confirmado en logs/olt_20260701_142328.log:6338-6341, donde 'save' se
// mandó sin salir de config y la OLT lo rechazó.
func (c *Conn) SaveConfig() error {
	if _, err := c.executeCommand("end", 10*time.Second); err != nil {
		return fmt.Errorf("saliendo a modo privilegiado: %w", err)
	}

	if _, err := c.executeCommand("copy running-config startup-config", 30*time.Second); err != nil {
		return err
	}
	time.Sleep(500 * time.Millisecond)
	if _, err := c.stdin.Write([]byte("y\r")); err != nil {
		return fmt.Errorf("confirmando guardado: %w", err)
	}
	time.Sleep(100 * time.Millisecond)
	c.readUntil(hashRe, c.cfg.Timeout)
	c.log("Configuración guardada")
	return nil
}

// Disconnect cierra la sesión SSH terminando el proceso sshpass.
func (c *Conn) Disconnect() error {
	if c.stdin != nil {
		c.stdin.Close()
		c.stdin = nil
	}
	if c.cmd != nil {
		if c.cmd.Process != nil {
			done := make(chan error, 1)
			go func() { done <- c.cmd.Wait() }()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				c.cmd.Process.Kill()
			}
		}
		c.cmd = nil
		c.log("Desconectado de la OLT")
	}
	return nil
}

// log hace eco de estado por pantalla.
func (c *Conn) log(message string) {
	fmt.Println(message)
}

// cleanANSI elimina las secuencias de escape ANSI de una respuesta.
func cleanANSI(s string) string {
	s = ansiRe1.ReplaceAllString(s, "")
	s = ansiRe2.ReplaceAllString(s, "")
	return s
}
