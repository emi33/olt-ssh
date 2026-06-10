// Package logger escribe un archivo de log por sesión, con marca de tiempo en
// cada línea. Es el equivalente de src/Logger.php de la versión PHP.
package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Logger escribe líneas con timestamp en un archivo de la carpeta de logs.
type Logger struct {
	file *os.File
	path string
}

// New crea (si hace falta) la carpeta de logs y abre un archivo único para esta
// sesión, con el nombre olt_<YYYYMMDD_HHMMSS>.log. Escribe la cabecera de inicio.
//
// Si logDir está vacío, se usa "logs".
func New(logDir string) (*Logger, error) {
	if logDir == "" {
		logDir = "logs"
	}
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, fmt.Errorf("creando carpeta de logs: %w", err)
	}

	// Nombre único por sesión basado en la fecha/hora de arranque.
	name := "olt_" + time.Now().Format("20060102_150405") + ".log"
	path := filepath.Join(logDir, name)

	// Abrimos en modo append (creando el archivo si no existe).
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("abriendo archivo de log: %w", err)
	}

	l := &Logger{file: f, path: path}
	l.Write("=== INICIO DE SESIÓN ===")
	l.Write("Fecha: " + time.Now().Format("2006-01-02 15:04:05"))
	return l, nil
}

// Write añade una línea al log con marca de tiempo y salto de línea.
func (l *Logger) Write(message string) {
	line := "[" + time.Now().Format("2006-01-02 15:04:05") + "] " + message + "\n"
	// Ignoramos el error de escritura: un fallo de log no debe abortar el registro.
	_, _ = l.file.WriteString(line)
}

// Command registra un comando enviado a la OLT y su respuesta, cerrando con "---".
func (l *Logger) Command(command, response string) {
	l.Write("COMANDO: " + command)
	if strings.TrimSpace(response) != "" {
		l.Write("RESPUESTA: " + strings.TrimSpace(response))
	}
	l.Write("---")
}

// Error escribe una línea marcada como error.
func (l *Logger) Error(message string) {
	l.Write("ERROR: " + message)
}

// Info escribe una línea informativa.
func (l *Logger) Info(message string) {
	l.Write("INFO: " + message)
}

// Path devuelve la ruta del archivo de log de esta sesión.
func (l *Logger) Path() string {
	return l.path
}

// Close escribe la línea de cierre y cierra el archivo.
func (l *Logger) Close() error {
	l.Write("=== FIN DE SESIÓN ===")
	return l.file.Close()
}
