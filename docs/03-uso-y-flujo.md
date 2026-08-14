# 3. Uso y flujo de ejecución

El punto de entrada es [`cmd/provisionar/main.go`](../cmd/provisionar/main.go).
Toda la lógica vive en la función `run()`, que devuelve el código de salida (se
separa de `main` para poder usar `defer` sin perder el valor de retorno).

## Las dos operaciones

El mismo programa hace dos cosas distintas según el flag, sobre el mismo conjunto
de registros:

| Comando | Qué hace | Fuente de datos |
|---------|----------|-----------------|
| `provisionar --registrar-onu` | da de alta las ONTs con `ont add` | `registro_onu` |
| `provisionar` (sin flag) | crea los `service-port` | `registro_cliente` |

El orden importa: un `service-port` solo puede referirse a una ONT que **ya** esté
dada de alta, así que primero se corre `--registrar-onu`.

## Modos de ejecución

### Modo dry-run (simulación, no toca la OLT)

```bash
go run ./cmd/provisionar --dry-run
# o:  ./provisionar --dry-run    (binario compilado)
```

En dry-run el programa carga la configuración, lee la BD, **arma los comandos**,
los muestra por pantalla y los **vuelca al log**, pero **no se conecta a la OLT**
ni ejecuta nada. Es el modo recomendado para una primera validación. Se acepta
tanto `--dry-run` como `-dry-run`.

### Ejecución real (pide confirmación)

```bash
go run ./cmd/provisionar
# o:  ./provisionar
```

Tras previsualizar los comandos, pide confirmación interactiva:

```text
¿Ejecutar estos comandos en la OLT? (s/N):
```

Solo continúa si se responde `s` (sin distinguir mayúsculas/espacios). Cualquier
otra respuesta cancela la operación (código de salida `0`).

## Flags

| Flag | Efecto |
|------|--------|
| `--dry-run` | muestra y loguea los comandos sin tocar la OLT |
| `--registrar-onu` | ejecuta el alta de ONTs (`ont add`) en vez de los `service-port` |
| `--sp-inicio=N` | base de índice a usar **solo** si la OLT no tiene ningún `service-port`; con service-ports existentes el índice arranca siempre en `max(ocupados)+1` |

## Flujo paso a paso

1. **Parseo de flags** — `--dry-run`, `--registrar-onu`, `--sp-inicio=N`.
2. **`config.Load()`** — carga env/`.env`. Si falta config requerida → `ERROR
   FATAL` y salida `1`.
3. **`database.New(cfg.DB)`** — abre MySQL y hace `Ping`. Si falla → salida `1`.
4. **`verificarOLTDestino()`** — guardrail: compara `OLT_HOST` con el `ipAdmin`
   del `PROVISIONING_ID_OLT` en la tabla `olt` y avisa si no coinciden.
5. **`db.GetRegistrosProvisioning(ctx, idOlt)`** — lee `registro_cliente` +
   `registro_onu` con `listo_para_cargar = 1`. Devuelve **datos**, no comandos.
6. **`seleccionarPendientes()`** — agrupa por ONU (`puerto` + `ont_id`) y **arma
   los comandos en Go**: un `ont add` por ONT y un `service-port` por cada VLAN
   de esa ONT. Las filas que no se pueden armar (sin SN, sin VLAN válida) salen
   en la lista de **omitidos**, con el motivo.
7. **Previsualización** — lista los comandos por pantalla; el índice del
   service-port aparece como `<auto>` porque todavía no se conoce.
8. **`logger.New("logs")`** — crea el archivo de log de la sesión y vuelca
   omitidos + comandos.
9. **Si dry-run** → termina acá (`0`).
10. **Confirmación** (`s/N`).
11. **Conexión** — `Connect` → `EnableMode` → `undo terminal monitor` /
    `undo terminal debugging` (para que las notificaciones asincrónicas del
    firmware no pisen el eco de los comandos) → `configure`.
12. **Ejecución** — `ejecutarRegistroOnu()` o `ejecutarServicePorts()` según el
    flag. En el segundo caso, antes se consulta `show service-port` para asignar
    índices libres.
13. **`SaveConfig()`** y resumen con el detalle de errores, en pantalla y en el log.

## Códigos de salida

| Código | Significado |
|--------|-------------|
| `0` | Éxito: todos los comandos se ejecutaron sin errores **o** no había nada que hacer (sin registros / operación cancelada / dry-run completado). |
| `1` | Error fatal (config, BD, conexión SSH) **o** la corrida terminó `CON ERRORES`. |

## Ejemplo de salida en consola

```text
==============================================
  Provisioning de Service-Port en OLT
==============================================

  Base de índice SP (solo si la OLT no tiene ninguno): 3699
  OLT destino: 192.168.25.1:22
  Base de datos: root@localhost:3306/olt_test

Conectando a la base de datos...
OLT destino verificada: idOlt=4 -> ipAdmin 192.168.25.1 coincide con OLT_HOST.

Ejecutando consulta de provisioning (idOlt=4)...
Registros obtenidos de la BD: 1209

Comandos a ejecutar en OLT (1047 registros):
--------------------------------------------
[1] MAC=A0:B1:C2:D3:E4:F5  plan=100 MB Hogar       traffic=1
    service-port <auto> config gpon 1/1/1 ont 1 gem-id 1 svlan 50 user-vlan 50 tag-action transparent traffic-in 1 traffic-out 1 desc 10235
...
--------------------------------------------

Log: logs/olt_20260805_133817.log

¿Ejecutar estos comandos en la OLT? (s/N): s

Conectando a la OLT...
Entrando en modo enable...
Entrando en modo configuración...
Consultando índices de service-port ocupados...
1033 índices ocupados detectados. Índices nuevos asignados: [...]

Ejecutando 1055 comandos service-port...
[1/1055] service-port 3699 config gpon 1/1/1 ont 1 gem-id 1 svlan 50 ...
  OK
...

==============================================
  RESULTADOS
==============================================
  Comandos ejecutados: 1055
  Errores:             0
  Omitidos:            0
  Estado: ÉXITO
  Log: logs/olt_20260805_133817.log
==============================================
```

## Errores y omitidos

Son dos listas distintas y ambas quedan en el log:

- **Omitidos** — registros que nunca llegaron a la OLT porque no se pudo armar el
  comando (sin SN, sin VLAN válida). Se muestran **antes** de la confirmación, así
  que también aparecen en dry-run.
- **Errores** — comandos que sí se enviaron y la OLT rechazó, bajo
  `=== DETALLE DE ERRORES (n) ===`. Un error **no detiene** la corrida: se sigue
  con el resto. Solo los fallos de transporte son fatales.

Para entender cómo se detectan los errores en las respuestas, ver
[07-logs-y-troubleshooting.md](07-logs-y-troubleshooting.md).
