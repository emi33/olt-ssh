# 1. Visión general

## ¿Qué hace el proyecto?

`olt-ssh-go` automatiza el **registro de clientes (ONTs)** en una **OLT GPON**.
El proceso es:

1. Consulta una base de datos **MySQL** (tablas `onu` y `eqcliente`) y, mediante
   una única consulta SQL, **genera** dos comandos por cada ONT:
   - `comando2` → `ont add ...` (da de alta la ONT en el puerto).
   - `comando1` → `service-port ...` (asocia VLAN/servicio a la ONT).
2. Se conecta por **SSH** a la consola interactiva de la OLT.
3. Ejecuta los comandos en **dos pasadas** y **guarda** la configuración.
4. Deja un **log por sesión** con cada comando y su respuesta.

Es una migración 1:1 de la versión PHP, escrita en **Go** (módulo `oltssh`,
Go ≥ 1.26) usando `golang.org/x/crypto/ssh` en lugar de phpseclib3.

## Glosario

| Término | Significado |
|---------|-------------|
| **OLT** | *Optical Line Terminal*. El equipo central de la red de fibra GPON al que nos conectamos por SSH. |
| **ONT / ONU** | *Optical Network Terminal/Unit*. El módem de fibra en casa del cliente. La tabla `onu` los lista. |
| **GPON** | Tecnología de fibra punto-multipunto. Cada puerto de la OLT se nombra `1/1/<n>`. |
| **GEM port** | Canal lógico GPON. La consulta calcula su id (`gem-id`) a partir de la VLAN del servicio. |
| **VLAN / svlan** | Identificador de red virtual del servicio. La `svlan` es la VLAN de servicio que se aplica en el `service-port`. |
| **service-port** | Línea de configuración que asocia una ONT + GEM + VLAN a un servicio. |
| **enable** | Modo privilegiado de la consola de la OLT (prompt `#`), necesario para configurar. |
| **dry-run** | Modo simulación: genera y muestra los comandos pero **no** toca la OLT. |

## Arquitectura por paquetes

El programa está dividido por responsabilidad (un paquete por área):

```text
cmd/registrar         Punto de entrada CLI: flags, dry-run, confirmación, resumen.
        │ usa
        ▼
internal/config       Carga y valida la configuración (env / .env).
internal/database     Abre MySQL y ejecuta la consulta generadora de comandos.
internal/logger       Escribe el log de sesión (logs/olt_*.log).
internal/olt          Conexión SSH (PTY) y ejecución de comandos en la OLT.
internal/registrar    Orquesta el proceso completo (depende de la OLT vía interfaz).
```

`internal/registrar` no depende del tipo concreto `*olt.Conn`, sino de una
**interfaz `OLT`**, lo que permite testearlo con un mock sin tocar una OLT real
(ver [04-componentes-go.md](04-componentes-go.md)).

## Diagrama de flujo

```text
   ┌──────────────┐
   │ cmd/registrar│  carga config, consulta BD, previsualiza comandos
   └──────┬───────┘
          │ (dry-run?) ──► sí ──► vuelca comandos al log y termina
          │ no
          ▼
   confirmación interactiva (s/N)
          │ s
          ▼
   ┌──────────────────┐   Run()
   │ internal/registrar│──────────────────────────────────────────┐
   └──────┬───────────┘                                           │
          │ Connect → EnableMode → configure → interface gpon     │
          │                                                       ▼
          │   PASADA 1: ont add  (todas las ONTs)         internal/olt
          │   exit                                        (SSH a la OLT)
          │   PASADA 2: service-port (todas las ONTs)            │
          │   SaveConfig                                         │
          ▼                                                      ▼
     Result (éxito / errores)                          logs/olt_*.log
```

## El orden importa: dos pasadas

El registro se hace en **dos pasadas separadas**, no entrelazadas:

1. **Pasada 1** — dentro de `interface gpon 1/1/<n>`, se ejecutan **todos** los
   `ont add` (alta de las ONTs).
2. Se sale de la interfaz (`exit`).
3. **Pasada 2** — ya en modo `config`, se ejecutan **todos** los `service-port`.

El motivo es que un `service-port` solo puede referirse a una ONT que **ya** esté
dada de alta; por eso primero se crean todas las ONTs y después se asocian sus
servicios. Esta lógica vive en [`registrar.Run()`](../internal/registrar/registrar.go).

Para entender cómo se generan los comandos, ver [05-consulta-sql.md](05-consulta-sql.md).
