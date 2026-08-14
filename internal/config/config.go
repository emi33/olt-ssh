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
	Timeout         time.Duration // timeout de conexión/lectura SSH (prompt inicial, enable, save)
	CommandTimeout  time.Duration // timeout de lectura por comando ejecutado (ExecuteCommand)
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

// QueryConfig son los parámetros que filtran las consultas SQL.
type QueryConfig struct {
	IDOlt             int // idOlt para la consulta de registrar/consultar (por puerto)
	PuertoOlt         int
	ProvisioningIDOlt int // idOlt de la consulta de provisioning (eqcliente en cmd/provisionar)

	// FechaDesde es la fecha de corte de la consulta principal/repaso: solo se
	// consideran observaciones (ct.fecha) y el corte de placeholders a partir de
	// esta fecha. Corresponde a la última corrida de read-olt. ENV
	// PROVISIONING_FECHA_DESDE (formato 'YYYY-MM-DD HH:MM:SS').
	FechaDesde string
	// Controladores es la lista de ipCT donde se buscan los clientes (los
	// concentradores). La mayoría de los clientes están ahí, pero es dinámica.
	// ENV PROVISIONING_CONTROLADORES (IPs separadas por coma).
	Controladores []string
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

// splitCSV parte una lista separada por comas, descartando espacios y entradas
// vacías. Se usa para PROVISIONING_CONTROLADORES (lista de ipCT).
func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
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
			CommandTimeout:  time.Duration(l.intOr("OLT_COMMAND_TIMEOUT", 5)) * time.Second,
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
			// QUERY_ID_OLT y QUERY_PUERTO_OLT son OPCIONALES: solo los usan los
			// comandos legacy 'registrar' y 'consultar' (flujo por puerto).
			// 'provisionar' NO los usa (trabaja con PROVISIONING_ID_OLT y saca el
			// puerto de cada fila), así que no se requieren para que arranque.
			IDOlt:             l.intOr("QUERY_ID_OLT", 0),
			PuertoOlt:         l.intOr("QUERY_PUERTO_OLT", 0),
			ProvisioningIDOlt: l.intOr("PROVISIONING_ID_OLT", 2),
			FechaDesde:        l.strOr("PROVISIONING_FECHA_DESDE", "2026-07-27 09:00:46"),
			Controladores:     splitCSV(l.strOr("PROVISIONING_CONTROLADORES", "172.16.4.14,172.16.4.5")),
		},
	}

	// La contraseña de enable cae a la de login si no se especifica (igual que el PHP).
	if cfg.OLT.EnablePassword == "" {
		cfg.OLT.EnablePassword = cfg.OLT.Password
	}

	// Un timeout por comando <= 0 no tiene sentido: caemos a 5s.
	if cfg.OLT.CommandTimeout <= 0 {
		cfg.OLT.CommandTimeout = 5 * time.Second
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
