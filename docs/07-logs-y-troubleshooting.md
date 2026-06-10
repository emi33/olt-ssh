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
lista de comandos que se *habrían* ejecutado (lo escribe `writeDryRunLog()` en
[`main.go`](../cmd/registrar/main.go)):

```text
[..] INFO: MODO DRY-RUN - Comandos NO ejecutados
[..] INFO: OLT: 192.168.2.102
[..] INFO: Puerto GPON: 1/1/16
[..] INFO: Total ONTs: 12
[..]
[..] ### COMANDOS ONT ADD ###
[..] ont add 1 sn-auth DF51-A63BCBD1 ...
[..]
[..] ### COMANDOS SERVICE-PORT ###
[..] service-port 3698 config gpon 1/1/16 ...
```

## Detección de errores

[`registrar.hasError()`](../internal/registrar/registrar.go) marca como error
cualquier respuesta que contenga (sin distinguir mayúsculas) alguna de estas
palabras:

```text
error, failure, failed, invalid, not found, already exists, conflict
```

Los comandos con error se acumulan en `Result.Errores` y se muestran al final de la
ejecución. **No detienen el proceso**: el programa intenta registrar todas las ONTs
y luego reporta cuáles fallaron. (En cambio, un fallo de **transporte** —p. ej. la
conexión SSH se cae— sí aborta y se registra como excepción).

## Problemas comunes

| Síntoma | Causa probable | Solución |
|---------|----------------|----------|
| `faltan variables de entorno requeridas: ...` | No están definidas todas las variables obligatorias. | Revisar el `.env` / entorno (ver [02-instalacion-y-configuracion.md](02-instalacion-y-configuracion.md)). |
| `variables de entorno con valor no numérico: ...` | Un `*_PORT`, `*_TIMEOUT` o `QUERY_*` trae texto no numérico. | Corregir el valor a un entero. |
| `error de conexión a la base de datos tras N intento(s)` | Credenciales/host de MySQL incorrectos o BD inaccesible tras agotar los reintentos. | Revisar las variables `DB_*` y la conectividad al puerto 3306. Para cortes transitorios, subir `DB_CONNECT_ATTEMPTS` / `DB_RETRY_DELAY`. |
| `error de conexión/autenticación SSH en la OLT tras N intento(s)` | Usuario/contraseña de la OLT incorrectos o host inalcanzable tras agotar los reintentos. | Revisar `OLT_HOST` / `OLT_USERNAME` / `OLT_PASSWORD`. Para cortes transitorios, subir `OLT_CONNECT_ATTEMPTS` / `OLT_RETRY_DELAY`. |
| La conexión SSH falla en la negociación o se cuelga | La OLT usa algoritmos no soportados, o exige `ssh-dss` (DSA) ya retirado de `x/crypto`. | Ver [06-conexion-ssh.md](06-conexion-ssh.md) y la versión de `golang.org/x/crypto` en [`go.mod`](../go.mod). |
| `No se encontraron ONTs para registrar.` | El filtro `QUERY_ID_OLT`/`QUERY_PUERTO_OLT` no devuelve filas. | Verificar esos valores y los datos de las tablas `onu`/`eqcliente`. |
| `already exists` en muchas respuestas | Las ONTs ya estaban dadas de alta. | Normal si se reejecuta; revisar si realmente hace falta volver a registrarlas. |
| Respuestas vacías o truncadas | La OLT tardó más que el timeout de lectura. | Aumentar `OLT_TIMEOUT` y/o los timeouts de `executeCommand`/`SaveConfig` en [`connection.go`](../internal/olt/connection.go). |
| `svlan 666` inesperado | La ONT no tenía VLAN válida en `eqcliente`. | Revisar la tabla `eqcliente` para ese cliente (ver [05-consulta-sql.md](05-consulta-sql.md)). |

## Recomendación de uso

1. Ejecutar **siempre primero** en `--dry-run` y revisar los comandos generados.
2. Comprobar que la numeración de `service-port` (contador `@x`, que arranca en
   3698) no choca con puertos ya configurados en la OLT.
3. Ejecutar en modo real solo tras validar el dry-run.
4. Conservar los logs como evidencia de los cambios aplicados.
