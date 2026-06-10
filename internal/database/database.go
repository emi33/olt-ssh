// Package database encapsula el acceso a MySQL y, sobre todo, la consulta que
// genera los comandos a ejecutar en la OLT. Es el equivalente de src/Database.php.
package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"oltssh/internal/config"

	_ "github.com/go-sql-driver/mysql"
)

// Comando es el par de comandos generado para cada ONT.
type Comando struct {
	Comando1 string // service-port ...
	Comando2 string // ont add ...
}

// DB envuelve la conexión a MySQL.
type DB struct {
	db *sql.DB
}

// New abre la conexión a MySQL a partir de la configuración 'database'.
//
// Verifica la conexión con un Ping y, si falla, reintenta hasta
// cfg.ConnectAttempts veces (esperando cfg.RetryDelay entre intentos). Esto cubre
// los casos en los que la base de datos aún no está lista o hay un corte de red
// transitorio. Si se agotan los intentos, devuelve el mensaje del último error.
func New(cfg config.DBConfig) (*DB, error) {
	// DSN de go-sql-driver/mysql: user:pass@tcp(host:port)/dbname?charset=...
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=%s",
		cfg.Username, cfg.Password, cfg.Host, cfg.Port, cfg.Name, cfg.Charset)

	attempts := cfg.ConnectAttempts
	if attempts < 1 {
		attempts = 1
	}

	var lastErr error
	for intento := 1; intento <= attempts; intento++ {
		db, err := sql.Open("mysql", dsn)
		if err != nil {
			// sql.Open no suele fallar (no abre conexión real); aun así lo tratamos
			// como intento fallido para no perder el detalle del error.
			lastErr = err
		} else if err := db.Ping(); err != nil {
			db.Close()
			lastErr = err
		} else {
			if intento > 1 {
				fmt.Printf("Conexión a la base de datos establecida en el intento %d/%d.\n", intento, attempts)
			}
			return &DB{db: db}, nil
		}

		fmt.Printf("Intento %d/%d de conexión a la base de datos fallido: %v\n", intento, attempts, lastErr)
		if intento < attempts {
			fmt.Printf("Reintentando en %s...\n", cfg.RetryDelay)
			time.Sleep(cfg.RetryDelay)
		}
	}

	return nil, fmt.Errorf("error de conexión a la base de datos tras %d intento(s): %w", attempts, lastErr)
}

// Close cierra el pool de conexiones.
func (d *DB) Close() error {
	return d.db.Close()
}

// GetComandosRegistro ejecuta la consulta que genera los comandos para registrar
// las ONTs del puerto indicado.
//
// La consulta usa la variable de usuario @x como contador correlativo. Para que
// su inicialización (en el CROSS JOIN) y su lectura ocurran en la MISMA conexión
// —y no en dos conexiones distintas del pool— se toma una conexión dedicada con
// db.Conn y se ejecuta todo sobre ella.
func (d *DB) GetComandosRegistro(ctx context.Context, idOlt, puertoOlt int) ([]Comando, error) {
	conn, err := d.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("obteniendo conexión dedicada: %w", err)
	}
	defer conn.Close()

	rows, err := conn.QueryContext(ctx, queryComandosRegistro, idOlt, puertoOlt)
	if err != nil {
		return nil, fmt.Errorf("ejecutando consulta de comandos: %w", err)
	}
	defer rows.Close()

	var comandos []Comando
	for rows.Next() {
		var c Comando
		if err := rows.Scan(&c.Comando1, &c.Comando2); err != nil {
			return nil, fmt.Errorf("leyendo fila de comandos: %w", err)
		}
		comandos = append(comandos, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterando filas de comandos: %w", err)
	}

	return comandos, nil
}
