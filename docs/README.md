# Documentación — OLT SSH (versión Go)

Esta carpeta contiene la documentación completa de la **versión Go** del proyecto
**OLT SSH**, una herramienta de línea de comandos que automatiza el registro de
clientes (ONTs) en una OLT GPON: consulta una base de datos MySQL para generar
los comandos, se conecta por SSH a la OLT y los ejecuta, dejando un log por sesión.

Es una **migración 1:1** de la [versión PHP](../../docs/README.md) (carpeta
hermana), con la misma lógica de negocio. Cuando un comportamiento es idéntico al
del PHP se indica y se enlaza al documento equivalente.

## Índice

| # | Documento | Contenido |
|---|-----------|-----------|
| 1 | [Visión general](01-vision-general.md) | Qué hace el proyecto, glosario, arquitectura por paquetes y diagrama de flujo. |
| 2 | [Instalación y configuración](02-instalacion-y-configuracion.md) | Requisitos, compilación con `go build` y variables de entorno (`.env`). |
| 3 | [Uso y flujo de ejecución](03-uso-y-flujo.md) | Cómo ejecutar, modo `--dry-run`, confirmación, códigos de salida y resultados. |
| 4 | [Componentes Go](04-componentes-go.md) | Descripción detallada de cada paquete (`cmd/`, `internal/*`) y sus tipos. |
| 5 | [La consulta SQL](05-consulta-sql.md) | Explicación de la consulta que genera los comandos de la OLT. |
| 6 | [Conexión SSH a la OLT](06-conexion-ssh.md) | Detalles de la conexión, modos (enable/config), PTY y algoritmos legacy. |
| 7 | [Logs y solución de problemas](07-logs-y-troubleshooting.md) | Formato de los logs y errores comunes. |
| 8 | [Mejoras](08-mejoras.md) | Reintentos de conexión (implementados) y hoja de ruta de mejoras propuestas. |

## Resumen rápido

```text
Base de datos MySQL (tabla onu + eqcliente)
        │
        ▼  consulta SQL -> genera comando1 (service-port) y comando2 (ont add)
cmd/registrar  ──►  internal/registrar  ──►  internal/olt (SSH / x/crypto)
        │                                              │
        │                                              ▼
        └────────────► internal/logger (logs/olt_*.log) ◄── OLT GPON
```

## Mapa de archivos del proyecto

```text
olt-ssh-go/
├── go.mod                        # Módulo 'oltssh', Go 1.26 y dependencias
├── go.sum                        # Checksums de dependencias
├── .env                          # Variables de entorno (ignorado por git)
├── README.md                     # Readme original del proyecto Go
├── docs/                         # ESTA documentación
├── logs/                         # Logs generados en cada ejecución
├── cmd/
│   └── registrar/main.go         # Punto de entrada / CLI principal
└── internal/
    ├── config/                   # Carga de configuración desde env/.env
    ├── database/                 # Conexión MySQL y consulta generadora de comandos
    ├── logger/                   # Registro de la sesión en archivo de log
    ├── olt/                      # Conexión SSH (PTY) y envío de comandos
    └── registrar/               # Orquesta el proceso de registro (+ tests)
```

> ℹ️ **Equivalencias con el PHP:** `cmd/registrar/main.go` ↔ `registrar_clientes.php`,
> `internal/database` ↔ `src/Database.php`, `internal/olt` ↔ `src/OltConnection.php`,
> `internal/registrar` ↔ `src/OltClientRegistrar.php`, `internal/logger` ↔ `src/Logger.php`.
