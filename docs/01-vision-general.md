# 1. Visión general

## ¿Qué hace el proyecto?

`olt-ssh-go` automatiza el **registro de clientes (ONTs)** en una **OLT GPON**.
El proceso es:

1. Lee las tablas **MySQL** `registro_onu` y `registro_cliente` (filas con
   `listo_para_cargar = 1`) — datos crudos, sin comandos.
2. **Arma los comandos en Go**, en `cmd/provisionar`:
   - `ont add ...` (da de alta la ONT en el puerto), a partir de `registro_onu`.
   - `service-port ...` (asocia VLAN/servicio a la ONT), a partir de
     `registro_cliente`; **uno por cada VLAN** de la ONT, porque la OLT no acepta
     varias VLANs en un mismo service-port.
3. Se conecta por **SSH** a la consola interactiva de la OLT.
4. Ejecuta una de las dos operaciones y **guarda** la configuración.
5. Deja un **log por sesión** con cada comando y su respuesta.

Escrito en **Go** (módulo `oltssh`, Go ≥ 1.26) usando `golang.org/x/crypto/ssh`.
Deriva de una versión PHP en la que los comandos se generaban dentro de una
consulta SQL; esa consulta ya no existe y su lógica vive ahora en Go, donde puede
testearse y consultar el estado real de la OLT.

## Glosario

| Término | Significado |
|---------|-------------|
| **OLT** | *Optical Line Terminal*. El equipo central de la red de fibra GPON al que nos conectamos por SSH. |
| **ONT / ONU** | *Optical Network Terminal/Unit*. El módem de fibra en casa del cliente. La tabla `onu` los lista. |
| **GPON** | Tecnología de fibra punto-multipunto. Cada puerto de la OLT se nombra `1/1/<n>`. |
| **GEM port** | Canal lógico GPON. Su id (`gem-id`) sale de la columna `gem_id` de `registro_cliente`. |
| **VLAN / svlan** | Identificador de red virtual del servicio. La `svlan` es la VLAN de servicio que se aplica en el `service-port`. |
| **service-port** | Línea de configuración que asocia una ONT + GEM + VLAN a un servicio. |
| **enable** | Modo privilegiado de la consola de la OLT (prompt `#`), necesario para configurar. |
| **dry-run** | Modo simulación: genera y muestra los comandos pero **no** toca la OLT. |

## Arquitectura por paquetes

El programa está dividido por responsabilidad (un paquete por área):

```text
cmd/provisionar       Punto de entrada CLI: lee la BD, ARMA los comandos, dry-run,
                      confirmación, ejecución y resumen.
        │ usa
        ▼
internal/config       Carga y valida la configuración (env / .env).
internal/database     Abre MySQL y lee las tablas registro_onu / registro_cliente.
internal/logger       Escribe el log de sesión (logs/olt_*.log).
internal/olt          Conexión SSH (PTY), ejecución de comandos y asignación de
                      índices de service-port libres.
```

Comandos auxiliares: `cmd/cargar` (llena las tablas `registro_*`),
`cmd/consultar` (inspección de ONUs de un puerto) y `cmd/repaso`
(clasificación de registros).

## Diagrama de flujo

```text
   ┌────────────────┐
   │ cmd/provisionar│  carga config, lee la BD, ARMA los comandos, previsualiza
   └──────┬─────────┘
          │ (dry-run?) ──► sí ──► vuelca comandos al log y termina
          │ no
          ▼
   confirmación interactiva (s/N)
          │ s
          ▼
   Connect → EnableMode → configure                          internal/olt
          │                                                  (SSH a la OLT)
          ├─ --registrar-onu:  por puerto,                          │
          │     interface gpon 1/1/<n> → ont add … → exit           │
          │                                                         │
          └─ (default):  show service-port (índices ocupados)       │
                → service-port <idx> config gpon …                  │
          │                                                         │
          ▼   SaveConfig                                            ▼
     ejecutados / errores                              logs/olt_*.log
```

## El orden importa: primero las ONTs

Las dos operaciones son **corridas separadas** del mismo programa, y el orden
entre ellas importa:

1. Primero `provisionar --registrar-onu` — dentro de `interface gpon 1/1/<n>`,
   da de alta **todas** las ONTs con `ont add`.
2. Después `provisionar` (sin flag) — ya en modo `config`, crea **todos** los
   `service-port`.

El motivo es que un `service-port` solo puede referirse a una ONT que **ya** esté
dada de alta. Los índices de service-port se asignan en vivo consultando
`show service-port` en la OLT, para no colisionar con los ya existentes (ver
[`internal/olt/serviceport.go`](../internal/olt/serviceport.go)).

Para entender cómo se arman los comandos, ver
[`seleccionarPendientes()`](../cmd/provisionar/main.go) y
[04-componentes-go.md](04-componentes-go.md).
