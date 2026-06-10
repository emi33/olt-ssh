# 2. Instalación y configuración

## Requisitos

- **Go >= 1.26** (definido en [`go.mod`](../go.mod)).
- Acceso de red a:
  - **MySQL** en el puerto `3306` (o el configurado).
  - La **OLT** por **SSH** en el puerto `22` (o el configurado).

Dependencias (se descargan solas, ver [`go.mod`](../go.mod)):

| Módulo | Uso |
|--------|-----|
| `github.com/go-sql-driver/mysql` | Driver MySQL para `database/sql`. |
| `github.com/joho/godotenv` | Carga del archivo `.env`. |
| `golang.org/x/crypto` | Cliente SSH (`golang.org/x/crypto/ssh`). |

## Instalación

```bash
go mod download   # descarga las dependencias
go build ./...    # compila todo el proyecto
```

Para generar un binario llamado `registrar`:

```bash
go build -o registrar ./cmd/registrar
```

## Configuración

A diferencia de la versión PHP (que usa un archivo `config.php` con un array), la
versión Go toma toda la configuración de **variables de entorno**. Si existe un
archivo `.env` en el directorio de trabajo, se carga automáticamente al arrancar
(las variables ya exportadas en el entorno tienen prioridad sobre el `.env`).

> ⚠️ **Diferencia con el PHP:** `config.php` → variables de entorno / `.env`.
> Las claves `olt`/`database`/`query_params` del PHP se corresponden con los
> prefijos `OLT_*`, `DB_*` y `QUERY_*` respectivamente.

La carga y validación viven en [`config.Load()`](../internal/config/config.go).

### Variables de entorno

| Variable | Por defecto | Requerida | Descripción |
|----------|-------------|-----------|-------------|
| `OLT_HOST` | — | **Sí** | IP/host de la OLT. |
| `OLT_PORT` | `22` | No | Puerto SSH. |
| `OLT_USERNAME` | — | **Sí** | Usuario SSH. |
| `OLT_PASSWORD` | — | **Sí** | Contraseña de login SSH. |
| `OLT_ENABLE_PASSWORD` | = `OLT_PASSWORD` | No | Contraseña del modo enable; si se deja vacía, se reutiliza `OLT_PASSWORD`. |
| `OLT_TIMEOUT` | `30` | No | Timeout SSH de conexión/lectura, en **segundos**. |
| `OLT_CONNECT_ATTEMPTS` | `3` | No | Nº de intentos de conexión SSH antes de rendirse (mínimo 1). |
| `OLT_RETRY_DELAY` | `2` | No | Espera entre intentos de conexión SSH, en **segundos**. |
| `DB_HOST` | — | **Sí** | Host de MySQL. |
| `DB_PORT` | `3306` | No | Puerto de MySQL. |
| `DB_NAME` | — | **Sí** | Nombre de la base de datos. |
| `DB_USERNAME` | — | **Sí** | Usuario de MySQL. |
| `DB_PASSWORD` | — | **Sí** | Contraseña de MySQL. |
| `DB_CHARSET` | `utf8mb4` | No | Charset de la conexión. |
| `DB_CONNECT_ATTEMPTS` | `3` | No | Nº de intentos de conexión a MySQL antes de rendirse (mínimo 1). |
| `DB_RETRY_DELAY` | `2` | No | Espera entre intentos de conexión a MySQL, en **segundos**. |
| `QUERY_ID_OLT` | — | **Sí** | Filtra qué OLT consultar (entero). |
| `QUERY_PUERTO_OLT` | — | **Sí** | Puerto GPON a registrar; se usa como `1/1/<n>` (entero). |

> ℹ️ La validación reporta **todos** los problemas a la vez: si faltan varias
> variables requeridas, el mensaje las lista juntas (`faltan variables de entorno
> requeridas: ...`). Si una variable que debe ser numérica (`*_PORT`, `*_TIMEOUT`,
> `QUERY_*`) trae texto no parseable, se informa como
> `variables de entorno con valor no numérico: ...`.

### Ejemplo de `.env`

Copia el `.env` de ejemplo y rellena los valores reales:

```bash
cp .env .env.local   # o edita el .env directamente
```

```dotenv
# --- Conexión SSH a la OLT ---
OLT_HOST=192.168.2.102
OLT_PORT=22
OLT_USERNAME=admin
OLT_PASSWORD=changeme
# Contraseña del modo enable; si se deja vacía, se reutiliza OLT_PASSWORD
OLT_ENABLE_PASSWORD=
OLT_TIMEOUT=30
# Reintentos de conexión SSH y espera (segundos) entre intentos
OLT_CONNECT_ATTEMPTS=3
OLT_RETRY_DELAY=2

# --- Conexión a la base de datos MySQL ---
DB_HOST=192.168.253.204
DB_PORT=3306
DB_NAME=olt
DB_USERNAME=emi
DB_PASSWORD=digitalNew
DB_CHARSET=utf8mb4
# Reintentos de conexión a MySQL y espera (segundos) entre intentos
DB_CONNECT_ATTEMPTS=3
DB_RETRY_DELAY=2

# --- Parámetros de la consulta ---
QUERY_ID_OLT=1
QUERY_PUERTO_OLT=16
```

> ⚠️ **Seguridad:** el `.env` contiene credenciales y está en `.gitignore`. **Nunca
> lo subas al repositorio.** La carpeta `logs/` también está ignorada por git.

Una vez configurado, sigue con [03-uso-y-flujo.md](03-uso-y-flujo.md).
