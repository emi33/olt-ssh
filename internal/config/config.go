// Package config carga la configuración del programa desde variables de entorno
// (opcionalmente desde un archivo .env). Reemplaza al config.php de la versión PHP.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// OLTConfig agrupa los datos de conexión SSH a la OLT.
type OLTConfig struct {
	Host            string
	Port            int
	Username        string
	Password        string
	EnablePassword  string        // si vacío en el entorno, se rellena con Password
	Timeout         time.Duration // timeout de conexión/lectura SSH
	ConnectAttempts int           // nº de intentos de conexión SSH antes de rendirse
	RetryDelay      time.Duration // espera entre intentos de conexión SSH
}

// DBConfig agrupa los datos de conexión a MySQL.
type DBConfig struct {
	Host            string
	Port            int
	Name            string
	Username        string
	Password        string
	Charset         string
	ConnectAttempts int           // nº de intentos de conexión a MySQL antes de rendirse
	RetryDelay      time.Duration // espera entre intentos de conexión a MySQL
}

// QueryConfig son los parámetros que filtran la consulta generadora de comandos.
type QueryConfig struct {
	IDOlt     int
	PuertoOlt int
}

// Config es la configuración completa del programa.
type Config struct {
	OLT   OLTConfig
	DB    DBConfig
	Query QueryConfig
}

// loader acumula los errores de validación para reportarlos todos juntos.
type loader struct {
	missing []string // variables requeridas que faltan
	invalid []string // variables con valor no parseable
}

// req devuelve el valor de una variable requerida; si falta, lo registra.
func (l *loader) req(key string) string {
	v := os.Getenv(key)
	if v == "" {
		l.missing = append(l.missing, key)
	}
	return v
}

// strOr devuelve el valor de la variable o un valor por defecto si está vacía.
func (l *loader) strOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// reqInt devuelve el valor entero de una variable requerida.
func (l *loader) reqInt(key string) int {
	v := os.Getenv(key)
	if v == "" {
		l.missing = append(l.missing, key)
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		l.invalid = append(l.invalid, key)
		return 0
	}
	return n
}

// intOr devuelve el valor entero de una variable o un valor por defecto.
func (l *loader) intOr(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		l.invalid = append(l.invalid, key)
		return def
	}
	return n
}

// Load lee la configuración del entorno. Si existe un archivo .env, lo carga
// primero (las variables ya exportadas en el entorno tienen prioridad).
func Load() (*Config, error) {
	// Ignoramos el error: que no haya .env es perfectamente válido.
	_ = godotenv.Load()

	l := &loader{}

	cfg := &Config{
		OLT: OLTConfig{
			Host:            l.req("OLT_HOST"),
			Port:            l.intOr("OLT_PORT", 22),
			Username:        l.req("OLT_USERNAME"),
			Password:        l.req("OLT_PASSWORD"),
			EnablePassword:  os.Getenv("OLT_ENABLE_PASSWORD"),
			Timeout:         time.Duration(l.intOr("OLT_TIMEOUT", 30)) * time.Second,
			ConnectAttempts: l.intOr("OLT_CONNECT_ATTEMPTS", 3),
			RetryDelay:      time.Duration(l.intOr("OLT_RETRY_DELAY", 2)) * time.Second,
		},
		DB: DBConfig{
			Host:            l.req("DB_HOST"),
			Port:            l.intOr("DB_PORT", 3306),
			Name:            l.req("DB_NAME"),
			Username:        l.req("DB_USERNAME"),
			Password:        l.req("DB_PASSWORD"),
			Charset:         l.strOr("DB_CHARSET", "utf8mb4"),
			ConnectAttempts: l.intOr("DB_CONNECT_ATTEMPTS", 3),
			RetryDelay:      time.Duration(l.intOr("DB_RETRY_DELAY", 2)) * time.Second,
		},
		Query: QueryConfig{
			IDOlt:     l.reqInt("QUERY_ID_OLT"),
			PuertoOlt: l.reqInt("QUERY_PUERTO_OLT"),
		},
	}

	// La contraseña de enable cae a la de login si no se especifica (igual que el PHP).
	if cfg.OLT.EnablePassword == "" {
		cfg.OLT.EnablePassword = cfg.OLT.Password
	}

	// Garantizamos al menos un intento de conexión, aunque el entorno traiga 0 o
	// un valor negativo (un "0 intentos" no tendría sentido).
	if cfg.OLT.ConnectAttempts < 1 {
		cfg.OLT.ConnectAttempts = 1
	}
	if cfg.DB.ConnectAttempts < 1 {
		cfg.DB.ConnectAttempts = 1
	}

	// Reportamos de una vez todos los problemas de configuración.
	if len(l.missing) > 0 {
		return nil, fmt.Errorf("faltan variables de entorno requeridas: %s", strings.Join(l.missing, ", "))
	}
	if len(l.invalid) > 0 {
		return nil, fmt.Errorf("variables de entorno con valor no numérico: %s", strings.Join(l.invalid, ", "))
	}

	return cfg, nil
}
