# 7. Logs y solución de problemas

## Dónde se guardan los logs

Cada ejecución (real o `--dry-run`) genera un archivo en la carpeta `logs/`:

```text
logs/olt_YYYYMMDD_HHMMSS.log      p. ej. logs/olt_20260609_200805.log
```

El nombre incluye la fecha y hora de inicio
(`time.Now().Format("20060102_150405")`), por lo que cada sesión tiene su propio
archivo. La carpeta se crea automáticamente si no existe (ver
[`logger.New()`](../internal/logger/logger.go)). La carpeta `logs/` está ignorada
por git.

## Formato del log

Cada línea lleva una marca de tiempo `[Y-m-d H:i:s]` (formato
`2006-01-02 15:04:05`):

```text
[2026-06-09 20:08:05] === INICIO DE SESIÓN ===
[2026-06-09 20:08:05] Fecha: 2026-06-09 20:08:05
[2026-06-09 20:08:05] COMANDO: configure
[2026-06-09 20:08:05] RESPUESTA: configure
DS-P7001-16_9E6174(config)#
[2026-06-09 20:08:05] ---
[2026-06-09 20:08:06] COMANDO: ont add 1 sn-auth DF51-A63BCBD1 ...
[2026-06-09 20:08:06] RESPUESTA: ...
[2026-06-09 20:08:06] ---
...
[2026-06-09 20:09:10] === FIN DE SESIÓN ===
```

| Línea | Origen |
|-------|--------|
| `=== INICIO DE SESIÓN ===` / `Fecha: ...` | Cabecera escrita por `logger.New()`. |
| `COMANDO: ...` / `RESPUESTA: ...` / `---` | Cada comando enviado a la OLT y su respuesta (`Logger.Command()`). |
| `INFO: ...` | Mensajes informativos (`Logger.Info()`, usados sobre todo en dry-run). |
| `ERROR: ...` | Errores registrados con `Logger.Error()`. |
| `=== FIN DE SESIÓN ===` | Cierre escrito por `Logger.Close()`. |

> ℹ️ La escritura del log ignora deliberadamente los errores de E/S: un fallo al
> escribir el log **no** debe abortar el registro de clientes.

### Log de un dry-run

En modo simulación el log no contiene respuestas de la OLT (no se conecta), sino la
lista de comandos que se *habrían* ejecutado, más los equipos omitidos
(lo escribe [`main.go`](../cmd/provisionar/main.go)):

```text
[..] INFO: Operación: Provisioning de Service-Port
[..] INFO: OLT destino: 192.168.25.1:22
[..] INFO: Registros en BD: 1209 | A procesar: 1047 | Omitidos: 0
[..]
[..] === COMANDOS A EJECUTAR ===
[..] service-port <auto> config gpon 1/1/1 ont 1 gem-id 1 svlan 50 ... desc 10235
[..]
[..] INFO: MODO DRY-RUN - OLT no modificada
```

El índice aparece como `<auto>` porque se asigna recién al conectar.

## Detección de errores

[`detectaError()`](../cmd/provisionar/main.go) marca como error cualquier
respuesta que contenga (sin distinguir mayúsculas) alguna de estas palabras:

```text
error, failure, failed, invalid, not found, already exists, conflict
```

Los comandos con error se acumulan y se vuelcan al final bajo
`=== DETALLE DE ERRORES (n) ===`, en pantalla y en el log. **No detienen el
proceso**: el programa intenta con todas las ONTs y luego reporta cuáles
fallaron. (En cambio, un fallo de **transporte** —p. ej. la conexión SSH se
cae— sí aborta la corrida).

Caso especial: `ecoCorrompido()` detecta un `Bad command` producido porque una
notificación asincrónica del firmware pisó el eco del comando (la OLT terminó
leyendo `ervice-port 4603 ...`). Cuando eso pasa el comando se **reintenta** una
vez antes de darlo por fallado. Para reducirlo de raíz, al conectar se envían
`undo terminal monitor` y `undo terminal debugging`.

## Problemas comunes

| Síntoma | Causa probable | Solución |
|---------|----------------|----------|
| `faltan variables de entorno requeridas: ...` | No están definidas todas las variables obligatorias. | Revisar el `.env` / entorno (ver [02-instalacion-y-configuracion.md](02-instalacion-y-configuracion.md)). |
| `variables de entorno con valor no numérico: ...` | Un `*_PORT`, `*_TIMEOUT` o `QUERY_*` trae texto no numérico. | Corregir el valor a un entero. |
| `error de conexión a la base de datos tras N intento(s)` | Credenciales/host de MySQL incorrectos o BD inaccesible tras agotar los reintentos. | Revisar las variables `DB_*` y la conectividad al puerto 3306. Para cortes transitorios, subir `DB_CONNECT_ATTEMPTS` / `DB_RETRY_DELAY`. |
| `error de conexión/autenticación SSH en la OLT tras N intento(s)` | Usuario/contraseña de la OLT incorrectos o host inalcanzable tras agotar los reintentos. | Revisar `OLT_HOST` / `OLT_USERNAME` / `OLT_PASSWORD`. Para cortes transitorios, subir `OLT_CONNECT_ATTEMPTS` / `OLT_RETRY_DELAY`. |
| La conexión SSH falla en la negociación o se cuelga | La OLT usa algoritmos no soportados, o exige `ssh-dss` (DSA) ya retirado de `x/crypto`. | Ver [06-conexion-ssh.md](06-conexion-ssh.md) y la versión de `golang.org/x/crypto` en [`go.mod`](../go.mod). |
| `No hay registros válidos para procesar.` | El filtro `PROVISIONING_ID_OLT` + `listo_para_cargar = 1` no devuelve filas. | Verificar ese valor y el contenido de `registro_onu` / `registro_cliente`. |
| `Error: Invalid parameter` en un `service-port` | La OLT acepta **una sola VLAN** por service-port. | Ya resuelto: `seleccionarPendientes()` emite un comando por VLAN. Si reaparece, revisar las VLANs de esa ONU en `registro_cliente`. |
| `Error: Missing parameter data` | El comando terminaba en `desc` sin valor. | Ya resuelto por `sufijoDesc()`, que omite el `desc` entero si no hay `nro_cliente` ni pppoe. |
| `Error: Bad command` con el eco cortado (`ervice-port ...`) | Una notificación asincrónica del firmware pisó el eco. | Se reintenta solo. Ver "Detección de errores" más arriba. |
| `already exists` en muchas respuestas | Las ONTs ya estaban dadas de alta. | Normal si se reejecuta; revisar si realmente hace falta volver a registrarlas. |
| Respuestas vacías o truncadas | La OLT tardó más que el timeout de lectura. | Aumentar `OLT_TIMEOUT` y/o los timeouts de `executeCommand`/`SaveConfig` en [`connection.go`](../internal/olt/connection.go). |
| Un equipo aparece en `EQUIPOS NO CARGADOS` | No se pudo armar el comando: sin SN o sin VLAN válida. | El motivo va en la misma línea del log. Revisar esa fila en `registro_onu` / `registro_cliente`. |

## Recomendación de uso

1. Ejecutar **siempre primero** en `--dry-run` y revisar los comandos generados.
2. Ejecutar en modo real solo tras validar el dry-run.
3. Conservar los logs como evidencia de los cambios aplicados.

**Nota:** hasta hace poco, el punto 2 incluía "comprobar a mano que la
numeración de `service-port` no choca con puertos ya configurados en la
OLT" — la numeración salía de un contador fijo (`PROVISIONING_SP_INICIO` /
`--sp-inicio`) sin verificar
el estado real de la OLT, así que dos corridas podían proponer el mismo índice. Esto
ya está resuelto: el índice se asigna en vivo, consultando `show
service-port` al conectar y evitando los índices ya ocupados (ver
[08-comandos-olt.md](08-comandos-olt.md), sección "Asignación de índices de
service-port"). El `--dry-run` ya no puede mostrar el índice final que se
va a usar — no se conecta a la OLT, así que no tiene forma de consultarlo
— y muestra `service-port <auto> ...` como placeholder.
