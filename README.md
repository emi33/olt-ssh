# OLT SSH (versión Go)

Herramienta en Go para registrar clientes (ONTs) en una OLT GPON. Lee las tablas
`registro_onu` y `registro_cliente` de MySQL, **arma los comandos en Go**
(`ont add` y `service-port`), se conecta por SSH a la OLT y los ejecuta, dejando
un log por sesión.

Mantiene la lógica de negocio de la versión PHP original: modo `--dry-run`,
confirmación interactiva, limpieza de ANSI y log por sesión. La generación de
comandos, que en PHP vivía en una consulta SQL, ahora es responsabilidad del
programa `provisionar`.

## Documentación

La documentación completa está en [`docs/`](docs/README.md): visión general,
instalación y configuración, uso y flujo, componentes Go, la consulta SQL, la
conexión SSH y logs/troubleshooting.

## Requisitos

- Go >= 1.26
- Acceso de red a MySQL (3306) y a la OLT por SSH (22)

## Instalación

```bash
go mod download
go build ./...
```

## Configuración

La configuración se toma de **variables de entorno**. Copia `.env.example` a
`.env` y rellena los valores (el `.env` se carga automáticamente al arrancar):

```bash
cp .env.example .env
# editar .env
```

| Variable | Por defecto | Descripción |
|----------|-------------|-------------|
| `OLT_HOST` | (requerida) | IP de la OLT |
| `OLT_PORT` | `22` | Puerto SSH |
| `OLT_USERNAME` / `OLT_PASSWORD` | (requeridas) | Credenciales SSH |
| `OLT_ENABLE_PASSWORD` | = `OLT_PASSWORD` | Contraseña del modo enable |
| `OLT_TIMEOUT` | `30` | Timeout SSH (segundos) |
| `OLT_CONNECT_ATTEMPTS` | `3` | Intentos de conexión SSH antes de rendirse |
| `OLT_RETRY_DELAY` | `2` | Espera entre intentos de conexión SSH (segundos) |
| `DB_HOST` | (requerida) | Host MySQL |
| `DB_PORT` | `3306` | Puerto MySQL |
| `DB_NAME` | (requerida) | Base de datos |
| `DB_USERNAME` / `DB_PASSWORD` | (requeridas) | Credenciales MySQL |
| `DB_CHARSET` | `utf8mb4` | Charset |
| `DB_CONNECT_ATTEMPTS` | `3` | Intentos de conexión a MySQL antes de rendirse |
| `DB_RETRY_DELAY` | `2` | Espera entre intentos de conexión a MySQL (segundos) |
| `PROVISIONING_ID_OLT` | (requerida) | `id_olt` de las tablas `registro_*` a provisionar |
| `PROVISIONING_SP_INICIO` | `3699` | Base de índice de service-port, solo si la OLT no tiene ninguno |
| `QUERY_ID_OLT` / `QUERY_PUERTO_OLT` | — | Solo los usa `cmd/consultar` (inspección por puerto) |

## Uso

### Modo dry-run (simulación, no toca la OLT)

```bash
go run ./cmd/provisionar --dry-run
```

### Ejecución real (pide confirmación)

```bash
# service-port (a partir de registro_cliente)
go run ./cmd/provisionar

# alta de ONTs con 'ont add' (a partir de registro_onu)
go run ./cmd/provisionar --registrar-onu
```

O con el binario compilado:

```bash
go build -o provisionar ./cmd/provisionar
./provisionar --dry-run
```

## Estructura

```
olt-ssh-go/
├── cmd/
│   ├── provisionar/          # Entry point principal: arma y ejecuta los comandos
│   ├── cargar/               # Llena las tablas registro_onu / registro_cliente
│   ├── consultar/            # Inspección de ONUs de un puerto
│   └── repaso/               # Clasificación / repaso de registros
└── internal/
    ├── config/               # Carga de configuración desde env/.env
    ├── logger/               # Log de sesión a archivo (logs/olt_*.log)
    ├── database/             # Conexión MySQL + lectura de las tablas registro_*
    ├── olt/                  # Conexión SSH (PTY) y asignación de índices de service-port
    └── spinner/              # Indicador de progreso en terminal
```

## Tests

```bash
go test ./...
```

Los tests cubren la asignación de índices de service-port, el parseo de
`show service-port` y la clasificación de registros.

## Notas

- Las credenciales viven en `.env` (ignorado por git). Nunca las subas al repo.
- Para OLTs antiguas se fuerzan algoritmos SSH legacy (KEX SHA1, cifrados CBC).
  Si la OLT exige host key DSA (`ssh-dss`), revisa la compatibilidad de la versión
  de `golang.org/x/crypto` (el soporte DSA se retiró en versiones recientes).
