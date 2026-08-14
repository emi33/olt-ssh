# Documentación — OLT SSH (versión Go)

Esta carpeta contiene la documentación completa de la **versión Go** del proyecto
**OLT SSH**, una herramienta de línea de comandos que automatiza el registro de
clientes (ONTs) en una OLT GPON: lee las tablas `registro_onu` / `registro_cliente`
de MySQL, arma los comandos **en Go**, se conecta por SSH a la OLT y los ejecuta,
dejando un log por sesión.

Deriva de una [versión PHP](../../../olt-php/docs/README.md) (carpeta hermana) en la que los
comandos se generaban dentro de una consulta SQL. Esa consulta ya no existe: el
armado vive ahora en `cmd/provisionar`.

## Índice

| # | Documento | Contenido |
|---|-----------|-----------|
| 1 | [Visión general](01-vision-general.md) | Qué hace el proyecto, glosario, arquitectura por paquetes y diagrama de flujo. |
| 2 | [Instalación y configuración](02-instalacion-y-configuracion.md) | Requisitos, compilación con `go build` y variables de entorno (`.env`). |
| 3 | [Uso y flujo de ejecución](03-uso-y-flujo.md) | Cómo ejecutar, modo `--dry-run`, confirmación, códigos de salida y resultados. |
| 4 | [Componentes Go](04-componentes-go.md) | Descripción detallada de cada paquete (`cmd/`, `internal/*`) y sus tipos. |
| 6 | [Conexión SSH a la OLT](06-conexion-ssh.md) | Detalles de la conexión, modos (enable/config), PTY y algoritmos legacy. |
| 7 | [Logs y solución de problemas](07-logs-y-troubleshooting.md) | Formato de los logs y errores comunes. |
| 8 | [Comandos de la OLT](08-comandos-olt.md) | Formato real de los comandos y respuestas de la OLT. |
| 9 | [Estructura del proyecto](09-estructura-del-proyecto.md) | Árbol completo y qué hace cada archivo. Mapa de referencia. |
| 10 | [Concurrencia y sistemas distribuidos](10-concurrencia-y-sistemas-distribuidos.md) | Documento de estudio: goroutines, canales, colas, microservicios y transacciones distribuidas, explicados sobre el código real del proyecto. |

## Resumen rápido

```text
Base de datos MySQL (registro_onu + registro_cliente)
        │
        ▼  datos crudos
cmd/provisionar  ──► arma 'ont add' y 'service-port' ──► internal/olt (SSH / x/crypto)
        │                                                        │
        │                                                        ▼
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
│   ├── provisionar/main.go       # Punto de entrada principal: arma y ejecuta comandos
│   ├── cargar/main.go            # Llena las tablas registro_onu / registro_cliente
│   ├── consultar/main.go         # Inspección de ONUs de un puerto (solo lectura)
│   └── repaso/main.go            # Clasificación / repaso de registros
└── internal/
    ├── config/                   # Carga de configuración desde env/.env
    ├── database/                 # Conexión MySQL y lectura de las tablas registro_*
    ├── logger/                   # Registro de la sesión en archivo de log
    ├── olt/                      # Conexión SSH (PTY), comandos e índices de service-port
    └── spinner/                  # Indicador de progreso en terminal
```

> ℹ️ **Equivalencias con el PHP:** `internal/database` ↔ `src/Database.php`,
> `internal/olt` ↔ `src/OltConnection.php`, `internal/logger` ↔ `src/Logger.php`.
> `cmd/provisionar` cubre lo que en PHP hacían `registrar_clientes.php` +
> `src/OltClientRegistrar.php` + la consulta generadora de comandos.
