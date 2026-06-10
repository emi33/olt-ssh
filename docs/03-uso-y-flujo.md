# 3. Uso y flujo de ejecución

El punto de entrada es [`cmd/registrar/main.go`](../cmd/registrar/main.go). Toda la
lógica vive en la función `run()`, que devuelve el código de salida (se separa de
`main` para poder usar `defer` sin perder el valor de retorno).

## Modos de ejecución

### Modo dry-run (simulación, no toca la OLT)

```bash
go run ./cmd/registrar --dry-run
# o:  ./registrar --dry-run    (binario compilado)
```

En dry-run el programa carga la configuración, consulta la BD, muestra los
comandos por pantalla y los **vuelca al log**, pero **no se conecta a la OLT** ni
ejecuta nada. Es el modo recomendado para una primera validación. Se acepta tanto
`--dry-run` como `-dry-run`.

### Ejecución real (pide confirmación)

```bash
go run ./cmd/registrar
# o:  ./registrar
```

Tras previsualizar los comandos, pide confirmación interactiva:

```text
¿Desea ejecutar estos comandos en la OLT? (s/N):
```

Solo continúa si se responde `s` (sin distinguir mayúsculas/espacios). Cualquier
otra respuesta cancela la operación (código de salida `0`).

## Flujo paso a paso

1. **Parseo de flags** — detecta `--dry-run`.
2. **`config.Load()`** — carga env/`.env`. Si falta config requerida → `ERROR
   FATAL` y salida `1`.
3. **`database.New(cfg.DB)`** — abre MySQL y hace `Ping`. Si falla → salida `1`.
4. **`db.GetComandosRegistro(ctx, idOlt, puertoOlt)`** — ejecuta la consulta y
   obtiene la lista de `Comando` (cada uno con `Comando1` y `Comando2`).
   - Si no hay filas → imprime *"No se encontraron ONTs para registrar."* y sale `0`.
5. **Previsualización** — `printComandos()` lista en pantalla los dos pasos:
   `### PASO 1: ONT ADD ###` y `### PASO 2: SERVICE-PORT ###`.
6. **Si dry-run** → `writeDryRunLog()` deja todo en el log y termina (`0`).
7. **Confirmación** (`s/N`).
8. **`logger.New("logs")`** — crea el archivo de log de la sesión.
9. **`olt.New(cfg.OLT, log)` + `registrar.New(conn, comandos, puertoOlt)`** y
   **`reg.Run()`** — ejecuta el proceso (ver [04-componentes-go.md](04-componentes-go.md)
   y [01-vision-general.md](01-vision-general.md) para las dos pasadas).
10. **`printResultados()`** — imprime el resumen y la ruta del log.

## Códigos de salida

| Código | Significado |
|--------|-------------|
| `0` | Éxito: todos los comandos se ejecutaron sin errores **o** no había nada que hacer (sin ONTs / operación cancelada / dry-run completado). |
| `1` | Error fatal (config, BD, etc.) **o** el proceso terminó `CON ERRORES` (`res.Success == false`). |

## Ejemplo de salida en consola

```text
==============================================
  Registro de Clientes en OLT
==============================================

Conectando a la base de datos...
Obteniendo comandos de registro...
Se encontraron 12 ONTs para registrar.

Comandos a ejecutar:
--------------------------------------------

### PASO 1: Comandos ONT ADD (en interface gpon) ###
[1] ont add 1 sn-auth DF51-A63BCBD1 ont-lineprofile-id 1 ont-srvprofile-id 1
...

### PASO 2: Comandos SERVICE-PORT (en config) ###
[1] service-port 3698 config gpon 1/1/16 ont 1 gem-id 1 svlan 666 user-vlan 666 tag-action transparent
...
--------------------------------------------

¿Desea ejecutar estos comandos en la OLT? (s/N): s

Iniciando registro de clientes...

Log guardado en: logs/olt_20260609_200805.log

... (progreso de cada comando) ...

==============================================
  RESULTADOS
==============================================
Comandos ejecutados: 24
Errores encontrados: 0
Estado: ÉXITO
Log: logs/olt_20260609_200805.log
```

## El resultado de `Run()`

`reg.Run()` devuelve un `registrar.Result` (no lanza error; los fallos se
acumulan, igual que el try/catch del PHP):

| Campo | Tipo | Significado |
|-------|------|-------------|
| `Success` | `bool` | `true` solo si no se acumuló ningún error. |
| `ComandosEjecutados` | `int` | Número de comandos enviados a la OLT. |
| `Errores` | `[]ErrItem` | Errores detectados. Si vienen de una respuesta de la OLT llevan `Comando`+`Respuesta`; si vienen de una excepción (fallo fatal de transporte) llevan `Mensaje` y `Tipo: "exception"`. |
| `Detalles` | `[]Detalle` | Traza de cada comando ejecutado con su respuesta y tipo (`ont_add` / `service_port`). |

`printResultados()` muestra el conteo, el estado (`ÉXITO` / `CON ERRORES`) y, si
los hay, el detalle de cada error. Para entender cómo se detectan los errores en
las respuestas, ver [07-logs-y-troubleshooting.md](07-logs-y-troubleshooting.md).
