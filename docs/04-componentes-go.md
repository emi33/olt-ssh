# 4. Componentes Go

El proyecto está organizado por responsabilidad: un paquete por área. Esta es la
descripción de cada uno, con sus tipos y funciones clave.

## `cmd/registrar` — punto de entrada

Archivo: [`cmd/registrar/main.go`](../cmd/registrar/main.go). Equivale a
`registrar_clientes.php`.

- `main()` → llama a `run()` y hace `os.Exit` con su código.
- `run() int` → lógica principal: flags, `config.Load`, `database.New`,
  `GetComandosRegistro`, previsualización, dry-run o confirmación, y `reg.Run()`.
- Helpers: `printComandos()`, `writeDryRunLog()`, `printResultados()`.

Detalles del flujo en [03-uso-y-flujo.md](03-uso-y-flujo.md).

## `internal/config` — configuración

Archivo: [`internal/config/config.go`](../internal/config/config.go). Reemplaza a
`config.php`.

**Tipos:**

| Tipo | Campos |
|------|--------|
| `OLTConfig` | `Host`, `Port`, `Username`, `Password`, `EnablePassword`, `Timeout` (`time.Duration`). |
| `DBConfig` | `Host`, `Port`, `Name`, `Username`, `Password`, `Charset`. |
| `QueryConfig` | `IDOlt`, `PuertoOlt`. |
| `Config` | Agrupa `OLT`, `DB` y `Query`. |

**Función clave:** `Load() (*Config, error)`. Carga el `.env` (si existe) y lee las
variables de entorno. Internamente usa un `loader` que acumula los errores
(`missing` / `invalid`) para reportarlos todos juntos. La contraseña de enable cae
a la de login si no se especifica. Ver tabla de variables en
[02-instalacion-y-configuracion.md](02-instalacion-y-configuracion.md).

## `internal/database` — MySQL y generación de comandos

Archivos: [`database.go`](../internal/database/database.go) y
[`query.go`](../internal/database/query.go). Equivale a `src/Database.php`.

**Tipos:**

- `Comando { Comando1, Comando2 string }` — el par de comandos por ONT
  (`Comando1` = `service-port ...`, `Comando2` = `ont add ...`).
- `DB` — envuelve el `*sql.DB`.

**Funciones clave:**

- `New(cfg config.DBConfig) (*DB, error)` — abre la conexión con el DSN
  `user:pass@tcp(host:port)/dbname?charset=...` y hace `Ping` para fallar de
  inmediato si las credenciales o el host no sirven.
- `GetComandosRegistro(ctx, idOlt, puertoOlt) ([]Comando, error)` — ejecuta la
  consulta generadora. Toma una **conexión dedicada** con `db.Conn(ctx)` para que
  la inicialización de la variable `@x` y su lectura ocurran en la **misma**
  conexión del pool. Pasa `idOlt` y `puertoOlt` como parámetros posicionales `?`.
- `Close()` — cierra el pool.

La consulta SQL se explica en detalle en [05-consulta-sql.md](05-consulta-sql.md).

## `internal/logger` — log de sesión

Archivo: [`internal/logger/logger.go`](../internal/logger/logger.go). Equivale a
`src/Logger.php`.

**Tipo:** `Logger` (envuelve un `*os.File` y la ruta).

**Funciones/métodos:**

| Método | Qué hace |
|--------|----------|
| `New(logDir string)` | Crea la carpeta (`logs` por defecto), abre `olt_<YYYYMMDD_HHMMSS>.log` en modo append y escribe la cabecera de inicio. |
| `Write(msg)` | Añade una línea con timestamp `[Y-m-d H:i:s]`. Ignora el error de escritura (un fallo de log no debe abortar el registro). |
| `Command(cmd, resp)` | Registra `COMANDO:`, `RESPUESTA:` (si no está vacía) y `---`. |
| `Error(msg)` / `Info(msg)` | Líneas con prefijo `ERROR:` / `INFO:`. |
| `Path()` | Ruta del archivo de log. |
| `Close()` | Escribe `=== FIN DE SESIÓN ===` y cierra el archivo. |

Formato y ejemplos en [07-logs-y-troubleshooting.md](07-logs-y-troubleshooting.md).

## `internal/olt` — conexión SSH

Archivo: [`internal/olt/connection.go`](../internal/olt/connection.go). Equivale a
`src/OltConnection.php`, usando `golang.org/x/crypto/ssh`.

**Tipo:** `Conn` — sesión SSH interactiva (cliente, sesión, `stdin`, canal de
lectura `readCh`, flag `inEnable`).

**Funciones/métodos:**

| Método | Qué hace |
|--------|----------|
| `New(cfg, log)` | Crea la conexión (todavía sin conectar); el logger es opcional. |
| `Connect()` | Abre SSH con algoritmos legacy, pide PTY (`vt100` 80x40), arranca el shell, lanza la goroutine lectora y consume el prompt inicial. |
| `EnableMode()` | Entra en modo privilegiado (`en`), enviando la contraseña de enable si la OLT la pide. **Idempotente**. |
| `ExecuteCommand(cmd)` | Envía un comando (timeout por defecto 10 s) y devuelve la respuesta limpia de ANSI. |
| `SaveConfig()` | Ejecuta `save` (timeout 30 s) y confirma con `y`. |
| `Disconnect()` | Cierra sesión y cliente. |

Detalles internos (PTY, `readUntil`, limpieza ANSI, prompts) en
[06-conexion-ssh.md](06-conexion-ssh.md).

## `internal/registrar` — orquestación

Archivos: [`registrar.go`](../internal/registrar/registrar.go) y
[`registrar_test.go`](../internal/registrar/registrar_test.go). Equivale a
`src/OltClientRegistrar.php`.

**Interfaz `OLT`** — la porción de la conexión que necesita el registrador
(`Connect`, `EnableMode`, `ExecuteCommand`, `SaveConfig`, `Disconnect`). La
satisface `*olt.Conn` y, en tests, un **mock**. Esto desacopla la orquestación de
la implementación SSH real.

**Tipos:** `Detalle` (traza comando/respuesta), `ErrItem` (error de respuesta o
excepción), `Result` (resultado global), `Registrar`.

**Funciones/métodos:**

- `New(o OLT, comandos []database.Comando, puertoOlt int) *Registrar`.
- `Run() Result` — el proceso completo: `Connect` → `EnableMode` → `configure` →
  `interface gpon 1/1/<n>` → **pasada 1** (todos los `ont add`) → `exit` →
  **pasada 2** (todos los `service-port`) → `SaveConfig`. La desconexión queda
  garantizada con `defer`. No retorna error: los fallos se acumulan en
  `Result.Errores`.
- `execTracked(res, tipo, cmd)` — ejecuta un comando, guarda el `Detalle`, e
  incrementa el contador; si la respuesta delata error (`hasError`), lo acumula.
  Solo devuelve error ante fallos de transporte (fatales).
- `hasError(resp)` — heurística de palabras clave (ver
  [07-logs-y-troubleshooting.md](07-logs-y-troubleshooting.md)).

**Tests** (`registrar_test.go`) — con un mock de `OLT` cubren: el orden de las dos
pasadas y la secuencia de salida, la detección de error en una respuesta (que **no**
detiene el proceso), el caso sin comandos (éxito sin conectar) y el error de
conexión (aborta y se reporta como excepción).

```bash
go test ./...
```

## Diagrama de relaciones

```text
        ┌───────────────┐
        │ cmd/registrar │
        └───┬───┬───┬───┘
            │   │   └──────────────► internal/logger
            │   │                          ▲
            │   └──► internal/config        │ (opcional)
            ▼                               │
   internal/database              internal/olt ──► OLT (SSH)
            ▲                               ▲
            │ []Comando                     │ interfaz OLT
            └────────► internal/registrar ──┘
```
