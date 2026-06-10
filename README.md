# OLT SSH (versión Go)

Reimplementación en Go de la herramienta de registro de clientes (ONTs) en una
OLT GPON. Consulta MySQL para generar los comandos (`ont add` y `service-port`),
se conecta por SSH a la OLT y los ejecuta, dejando un log por sesión.

Es una migración 1:1 de la versión PHP (carpeta hermana), con la misma lógica de
negocio: modo `--dry-run`, confirmación interactiva, dos pasadas
(`ont add` → `service-port`), limpieza de ANSI y log por sesión.

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
| `QUERY_ID_OLT` | (requerida) | Filtra la OLT a consultar |
| `QUERY_PUERTO_OLT` | (requerida) | Puerto GPON (forma `1/1/<n>`) |

## Uso

### Modo dry-run (simulación, no toca la OLT)

```bash
go run ./cmd/registrar --dry-run
```

### Ejecución real (pide confirmación)

```bash
go run ./cmd/registrar
```

O con el binario compilado:

```bash
go build -o registrar ./cmd/registrar
./registrar --dry-run
```

## Estructura

```
olt-ssh-go/
├── cmd/registrar/main.go     # Entry point: flags, dry-run, confirmación, resumen
└── internal/
    ├── config/               # Carga de configuración desde env/.env
    ├── logger/               # Log de sesión a archivo (logs/olt_*.log)
    ├── database/             # Conexión MySQL + consulta generadora de comandos
    ├── olt/                  # Conexión SSH (PTY) y ejecución de comandos
    └── registrar/            # Orquestación (interfaz OLT) + tests
```

## Tests

```bash
go test ./...
```

Los tests cubren el orden de las dos pasadas, la detección de errores y los casos
límite del registrador (con un mock de la conexión SSH).

## Notas

- Las credenciales viven en `.env` (ignorado por git). Nunca las subas al repo.
- Para OLTs antiguas se fuerzan algoritmos SSH legacy (KEX SHA1, cifrados CBC).
  Si la OLT exige host key DSA (`ssh-dss`), revisa la compatibilidad de la versión
  de `golang.org/x/crypto` (el soporte DSA se retiró en versiones recientes).
