# 4. Componentes Go

El proyecto está organizado por responsabilidad: un paquete por área. Esta es la
descripción de cada uno, con sus tipos y funciones clave.

## `cmd/provisionar` — punto de entrada y generación de comandos

Archivo: [`cmd/provisionar/main.go`](../cmd/provisionar/main.go). Es el único
entry point del flujo de carga y, además de orquestar, **arma los comandos**
(antes los generaba una consulta SQL).

- `main()` → llama a `run()` y hace `os.Exit` con su código.
- `run() int` → lógica principal: flags (`--dry-run`, `--registrar-onu`,
  `--sp-inicio=N`), `config.Load`, `database.New`,
  `GetRegistrosProvisioning`, filtrado, previsualización, dry-run o
  confirmación, y ejecución en la OLT.
- `seleccionarPendientes(registros)` → **el corazón de la generación**: agrupa
  las filas por ONU (`puerto` + `ont_id`), junta sus VLANs y produce por cada
  ONT un `ont add ...` y **un `service-port` por VLAN** (la OLT rechaza
  `svlan 50,65` con `Invalid parameter`). Devuelve también la lista de omitidos
  con el motivo.
- `ejecutarRegistroOnu(...)` → agrupa por puerto, entra a
  `interface gpon 1/1/<n>`, ejecuta los `ont add` y sale con `exit`.
- `ejecutarServicePorts(...)` → consulta los índices ocupados en vivo, asigna
  los libres y ejecuta un `service-port` por sufijo.
- Helpers de armado: `sufijoDesc()` (omite `desc` si no hay valor — un `desc`
  vacío hace que la OLT rechace el comando entero), `formatearSN()`,
  `codigoTrafico()`, `descripcion()`, `detectaError()` y `ecoCorrompido()`
  (detecta el eco pisado por mensajes asíncronos del firmware y reintenta).

Detalles del flujo en [03-uso-y-flujo.md](03-uso-y-flujo.md).

## `internal/config` — configuración

Archivo: [`internal/config/config.go`](../internal/config/config.go). Reemplaza a
`config.php`.

**Tipos:**

| Tipo | Campos |
|------|--------|
| `OLTConfig` | `Host`, `Port`, `Username`, `Password`, `EnablePassword`, `Timeout` (`time.Duration`). |
| `DBConfig` | `Host`, `Port`, `Name`, `Username`, `Password`, `Charset`. |
| `QueryConfig` | `ProvisioningIDOlt`, `FechaDesde`, `Controladores` y, solo para `cmd/consultar`, `IDOlt` / `PuertoOlt`. |
| `Config` | Agrupa `OLT`, `DB` y `Query`. |

**Función clave:** `Load() (*Config, error)`. Carga el `.env` (si existe) y lee las
variables de entorno. Internamente usa un `loader` que acumula los errores
(`missing` / `invalid`) para reportarlos todos juntos. La contraseña de enable cae
a la de login si no se especifica. Ver tabla de variables en
[02-instalacion-y-configuracion.md](02-instalacion-y-configuracion.md).

## `internal/database` — MySQL (solo lectura de datos)

Archivos: [`database.go`](../internal/database/database.go) y
[`query.go`](../internal/database/query.go).

Este paquete **ya no genera comandos**: devuelve datos crudos y el armado del
comando es responsabilidad de `cmd/provisionar`. La antigua constante
`queryComandosRegistro`, que construía `comando1`/`comando2` con `CONCAT` en
SQL, fue eliminada.

**Tipos:**

- `RegistroServicio` — una fila de `registro_cliente` cruzada con su
  `registro_onu` (MAC, cliente, VLAN, puerto, `ont_id`, `gem_id`, plan, SN,
  estado de presencia…).
- `ONUInfo`, `FilaCarga`, `RegistroOnu`, `RegistroCliente` — tipos de los flujos
  de consulta y carga.
- `DB` — envuelve el `*sql.DB`.

**Funciones clave:**

- `New(cfg config.DBConfig) (*DB, error)` — abre la conexión con el DSN
  `user:pass@tcp(host:port)/dbname?charset=...` y hace `Ping` para fallar de
  inmediato si las credenciales o el host no sirven.
- `GetRegistrosProvisioning(ctx, idOlt) ([]RegistroServicio, error)` — lee
  `registro_cliente` + `registro_onu` con `listo_para_cargar = 1`
  (constante `queryRegistrosProvisioning`). Es la fuente de `cmd/provisionar`.
- `GetFilasCargaPrincipal(...)` / `ReemplazarRegistros(...)` — el flujo de
  `cmd/cargar`, que llena las tablas `registro_*`.
- `GetONUsDelPuerto(...)` — inspección por puerto (`cmd/consultar`).
- `Close()` — cierra el pool.

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

Archivos: [`internal/olt/connection.go`](../internal/olt/connection.go)
(conexión base) y [`internal/olt/serviceport.go`](../internal/olt/serviceport.go)
+ [`internal/olt/allocator.go`](../internal/olt/allocator.go) (asignación de
índices de `service-port`). `connection.go` equivale a `src/OltConnection.php`,
usando `golang.org/x/crypto/ssh`.

**Tipo:** `Conn` — sesión SSH interactiva (cliente, sesión, `stdin`, canal de
lectura `readCh`, flag `inEnable`).

**Funciones/métodos:**

| Método | Qué hace |
|--------|----------|
| `New(cfg, log)` | Crea la conexión (todavía sin conectar); el logger es opcional. |
| `Connect()` | Abre SSH con algoritmos legacy, pide PTY (`vt100` 80x40), arranca el shell, lanza la goroutine lectora y consume el prompt inicial. |
| `EnableMode()` | Entra en modo privilegiado (`en`), enviando la contraseña de enable si la OLT la pide. **Idempotente**. |
| `ExecuteCommand(cmd)` | Envía un comando (timeout por defecto 10 s) y devuelve la respuesta limpia de ANSI. |
| `IndicesServicePortOcupados()` | Consulta `show service-port` (con manejo defensivo de paginación) y devuelve el set de índices ya usados en la OLT. |
| `SaveConfig()` | Sale a modo privilegiado con `end` y ejecuta `copy running-config startup-config` (timeout 30 s), confirmando con `y`. |
| `Disconnect()` | Cierra sesión y cliente. |

`SiguienteIndicesLibres(ocupados, cantidad, base)` (función libre en
`allocator.go`, sin estado ni SSH) calcula los próximos índices libres a
partir de `max(ocupados)+1`. Ver [08-comandos-olt.md](08-comandos-olt.md),
sección "Asignación de índices de service-port".

Detalles internos (PTY, `readUntil`, limpieza ANSI, prompts) en
[06-conexion-ssh.md](06-conexion-ssh.md).

## Tests

```bash
go test ./...
```

Cubren la asignación de índices de service-port (`internal/olt/allocator_test.go`),
el parseo de `show service-port` (`serviceport_test.go`) y la clasificación de
registros (`internal/database/clasificador*_test.go`).

## Diagrama de relaciones

```text
        ┌────────────────┐
        │ cmd/provisionar│  (arma los comandos)
        └───┬───┬───┬────┘
            │   │   └──────────────► internal/logger
            │   │                          ▲
            │   └──► internal/config        │ (opcional)
            ▼                               │
   internal/database              internal/olt ──► OLT (SSH)
   (registro_onu /                 (SSH + índices
    registro_cliente)               de service-port)
```
