# 9. Estructura del proyecto

Mapa completo de archivos y qué hace cada uno. Es el documento de referencia para
ubicarse rápido; el detalle de cada tema vive en los documentos 1–8.

## Árbol

```text
olt-ssh-go/
├── cmd/                         4 ejecutables independientes (un main por carpeta)
│   ├── cargar/main.go              1ª pasada de carga a las tablas de registro
│   ├── repaso/main.go              2ª pasada (repaso)
│   ├── provisionar/main.go         ejecuta service-port / ont add en la OLT
│   └── consultar/main.go           sólo lectura (comandos show)
├── internal/
│   ├── config/config.go            carga de .env y variables de entorno
│   ├── database/
│   │   ├── database.go             conexión MySQL + todas las consultas + escritura
│   │   ├── query.go                las 2 consultas SQL grandes, como constantes
│   │   ├── clasificador.go         tipos de registro + clasificación 1ª pasada
│   │   ├── clasificador_repaso.go  clasificación del repaso (la lógica más densa)
│   │   ├── clasificador_test.go
│   │   └── clasificador_repaso_test.go
│   ├── olt/
│   │   ├── connection.go           sesión SSH interactiva (sshpass + ssh externo)
│   │   ├── serviceport.go          'show service-port' con paginación + parseo
│   │   ├── allocator.go            cálculo de índices de service-port libres
│   │   ├── serviceport_test.go
│   │   └── allocator_test.go
│   ├── logger/logger.go            archivo de log por sesión
│   └── spinner/spinner.go          indicador de carga en consola
├── sql/
│   ├── registro_migraciones.sql            DDL de las tablas + consultas de referencia
│   └── registro_migraciones_multivlan.sql  migración multi-vlan de registro_cliente
├── docs/                        esta documentación
├── logs/                        un archivo por corrida (generado, no versionado)
├── .env                         credenciales reales (NO versionado)
├── .env example                 plantilla de variables
├── go.mod / go.sum
└── README.md
```

---

## `cmd/` — los cinco ejecutables

Cada carpeta es un binario aparte. Se corren con `go run ./cmd/<nombre>` o se
compilan con `go build ./cmd/<nombre>`.

| Comando | ¿Toca la OLT? | Qué hace |
|---|---|---|
| `cargar` | no | 1ª pasada: llena las tablas de registro desde los clientes activos en CT |
| `repaso` | no | 2ª pasada: completa y corrige desde el histórico de MACs de la OLT |
| `provisionar` | **sí, escribe** | ejecuta `service-port` / `ont add` por SSH |
| `consultar` | sí, sólo lee | ejecuta comandos `show` con confirmación por comando |

### `cmd/cargar/main.go` — primera pasada

Corre `queryCargaPrincipal`: clientes **activos** (`ct.activo = 1`) en los CT
configurados, cruzados con la última aparición de cada MAC en `eqcliente` y con su
ONU. Clasifica con `ClasificarPrimeraPasada` y reemplaza las tablas de registro de
la OLT en una transacción. No se conecta a la OLT.

Va "desde el cliente": sólo alcanza MACs que tienen cliente vigente.

Flag: `--dry-run` (clasifica e imprime el resumen sin escribir).

### `cmd/repaso/main.go` — segunda pasada

Invierte la dirección: arranca **desde la OLT** (`onu` + `eqcliente`) y trata de
explicar cada posición, tenga o no cliente. Es el comando con más lógica dentro de
`main()`:

- arma el acumulador por `(posición, mac, vlan)` y resuelve el cliente de cada
  servicio (regla de "exactamente un activo, el resto inactivos");
- depura con `database.DedupMacs` y `database.PlaceholdersLibres`;
- clasifica con `ClasificarRepaso` y reemplaza las tablas en una transacción,
  incluyendo el relleno final de ONUs faltantes.

Flag: `--dry-run`.

### `cmd/provisionar/main.go` — el que escribe en la OLT

Dos operaciones sobre el mismo conjunto filtrado, para que la cantidad de
`ont add` y de `service-port` coincida siempre:

- por defecto crea los **service-port**;
- con `--registrar-onu` da de alta las ONTs con `ont add`, agrupando por puerto
  (entra a `interface gpon 1/1/<p>`, ejecuta y sale).

Incluye el guardrail `verificarOLTDestino`, que aborta si `OLT_HOST` no coincide
con el `ipAdmin` del `PROVISIONING_ID_OLT` — evita configurar la OLT equivocada.
Los índices de service-port se asignan **en vivo** tras consultar la OLT. Pide
confirmación interactiva antes de ejecutar.

Flags: `--dry-run`, `--registrar-onu`, `--sp-inicio=N`.

### `cmd/consultar/main.go` — sólo lectura

Se conecta y ejecuta únicamente comandos `show`, mostrando cada uno y pidiendo
confirmación antes de mandarlo. No modifica nada.

---

## `internal/config`

**`config.go`** — carga el `.env` (si existe) y lee las variables de entorno. Las
ya exportadas en el entorno tienen prioridad sobre el archivo.

Tipos: `OLTConfig` (host, credenciales, timeouts, reintentos), `DBConfig`,
`QueryConfig` (`ProvisioningIDOlt`, `FechaDesde`, `Controladores`, y los
`IDOlt`/`PuertoOlt` que sólo usa `consultar`), y `Config` que los agrupa.

`Load()` usa un `loader` interno que acumula las variables faltantes y las no
numéricas para reportarlas **todas juntas** en un solo error, en vez de fallar en
la primera.

---

## `internal/database`

El paquete más grande. Mezcla acceso a datos (`database.go`, `query.go`) con
lógica de clasificación pura (`clasificador*.go`).

### `database.go`

Conexión y todas las operaciones contra MySQL.

| Función | Para qué |
|---|---|
| `New` | abre el pool con reintentos configurables |
| `GetONUsDelPuerto`, `GetOLTIPAdmin` | consultas puntuales; la segunda alimenta el guardrail |
| `GetFilasCargaPrincipal` | la consulta de la 1ª pasada (arma el `IN (...)` de controladores) |
| `GetPlanesPorNombre`, `GetGemPortPorVlan` | tablas de lookup (plan → id, vlan → gem) |
| `GetOnusIdOlt` | universo de ONUs de la OLT |
| `GetMacsHistoricasRepaso` | **el corazón del repaso**: última aparición de cada servicio `(mac, vlan)` con todos sus matches en CT |
| `GetPlaceholderIdx` | índices con MAC `00:00` por posición |
| `GetPosicionesPrincipal` | estado con que la 1ª pasada dejó cada posición |
| `ReemplazarRegistros` | borra lo anterior de la OLT y escribe lo nuevo, **todo en una transacción** |
| `ContarOnu`, `ContarRegistroOnu` | verificación de que las tablas quedaron parejas |

Tipos de fila: `RegistroServicio`, `FilaCarga`, `OnuBasica`, `MacRepaso`,
`EstadoPrevio`, `ResultadoEscritura`.

### `query.go`

Las dos consultas SQL largas como constantes, con su explicación:

- `queryRegistrosProvisioning` — alimenta `cmd/provisionar`: cruza
  `registro_cliente` con `registro_onu` filtrando `listo_para_cargar = 1`.
- `queryCargaPrincipal` — la 1ª pasada; el `%s` se reemplaza en Go por la lista de
  `?` de los controladores.

Ninguna genera comandos: devuelven datos y el armado es de `cmd/provisionar`.
La antigua `queryComandosRegistro`, que construía `comando1`/`comando2` con
`CONCAT` en SQL para el flujo `cmd/registrar`, fue eliminada junto con ese
comando.

### `clasificador.go`

Tipos que se escriben en la BD (`RegistroOnu`, `RegistroCliente`), las constantes
de estado `principal_*`, y `ClasificarPrimeraPasada`, que agrupa las filas por ONU
y decide el estado de cada una.

Contiene además los helpers que comparten las dos pasadas: `snParaRegistro`
(normaliza SN vacío/`UNKNOWN` a NULL para no chocar con el índice único),
`buscarPlan`, `buscarGem`, `vlanEsCamara`, `clasificarFila`.

### `clasificador_repaso.go`

La lógica más densa del proyecto, toda pura y testeable sin BD.

- Constantes de los casos del repaso (`sin_cliente_en_ct`, `vlan_mayor_3000_*`,
  `onu_mac_multiservicio`, `clientes_ct_ambiguos`, …).
- `MacInfo` — un servicio `(mac, vlan)` con su cliente ya resuelto y los flags del
  matcheo contra CT.
- `OnuRepaso` — una posición completa lista para clasificar.
- `ClasificarRepaso` — devuelve la fila `registro_onu` y sus `registro_cliente`,
  aplicando el prefijo `repaso_`/`sobrescrito_repaso_` y la cascada de estado.
- `clasificarOnuRepaso` — el `switch` con la precedencia de casos.
- `DedupMacs` — resuelve entradas repetidas dentro de una posición.
- `PlaceholdersLibres` — descarta los `00:00` cuyo índice ya ocupa una MAC real.
- Helpers de motivo (`motivoMac`, `motivoMultiservicio`, `motivoPlaceholders`,
  `unirMotivos`) y de conteo (`macsDistintas`, `macsDistintasConCliente`).

### Tests

`clasificador_test.go` y `clasificador_repaso_test.go` cubren la clasificación de
las dos pasadas, el dedup y el filtrado de placeholders. Son tests puros, no
necesitan base de datos.

---

## `internal/olt`

### `connection.go`

Sesión SSH interactiva con la OLT. **No usa `golang.org/x/crypto/ssh`**: invoca
`sshpass` + `ssh` externos porque la OLT presenta una clave DSA-512 que la
librería de Go rechaza por debajo del mínimo de 1024 bits.

`Connect` (con reintentos), `EnableMode` (idempotente), `ExecuteCommand`,
`SaveConfig`, `Disconnect`. Internamente: una goroutine lee el stdout en trozos
hacia un canal, `readUntil` acumula hasta que casa el prompt, `drain` descarta
restos del comando anterior y `cleanANSI` limpia las secuencias de escape.

### `serviceport.go`

`IndicesServicePortOcupados` corre `show service-port` y devuelve el set de
índices ya usados. Maneja la paginación (`-- More --`) mandando espacios hasta
llegar al prompt. `parseIndicesServicePort` está separada para poder testear el
parseo sin conexión.

### `allocator.go`

`SiguienteIndicesLibres(ocupados, cantidad, base)` — calcula N índices libres
partiendo de `max(ocupados)+1`, o de `base` si la OLT no tiene ninguno.

---

## `internal/logger` y `internal/spinner`

- **`logger.go`** — un archivo por sesión en `logs/olt_<YYYYMMDD_HHMMSS>.log`, con
  timestamp por línea. `Write`, `Info`, `Error`, `Command` (comando + respuesta).
  Los errores de escritura se ignoran a propósito: un fallo de log no debe abortar
  un registro en curso.
- **`spinner.go`** — indicador giratorio para operaciones lentas. `Start(msg)` /
  `Stop()`.

---

## `sql/`

- **`registro_migraciones.sql`** — DDL de `planes`, `gem_config`, `registro_onu` y
  `registro_cliente`, más el catálogo de consultas de referencia. Es el esquema
  vivo del flujo de carga.
- **`registro_migraciones_multivlan.sql`** — migración que permite guardar la misma
  MAC con varias vlans: `uq_olt_mac` pasa a `uq_olt_mac_vlan (id_olt, mac_wan,
  vlan)`, se elimina `uq_olt_pppoe` y `vlan` pasa a `NOT NULL DEFAULT 0`.

---

## Raíz

| Archivo | Qué es |
|---|---|
| `.env` | credenciales reales. **No versionado** (está en `.gitignore`). |
| `.env example` | plantilla con todas las variables y sus valores por defecto. |
| `go.mod` / `go.sum` | módulo `oltssh`, Go 1.25. Dependencias: `go-sql-driver/mysql` y `joho/godotenv`. |
| `README.md` | resumen de instalación y uso. |
| `logs/` | generado en cada corrida, no versionado. |

---

## Mapa de dependencias

```text
cmd/cargar ─┐
cmd/repaso ─┴──► internal/database ──► MySQL
                        │
cmd/provisionar ────────┼──► internal/olt ──► sshpass/ssh ──► OLT GPON
      │                 │           │
      │                 └──► internal/config
      └──────────────────────► internal/logger

cmd/consultar ──► internal/olt
```

`internal/database` no conoce a `internal/olt` ni al revés: la BD y la OLT sólo se
encuentran en los `cmd/`. `config`, `logger` y `spinner` no dependen de nada del
proyecto.

---

## Estado del código — cosas a tener presentes

Anotado acá para que no sorprenda al leer los archivos:

- **`docs/04-componentes-go.md` describe `provisionar` y los paquetes internos**,
  pero no entra en `cargar`, `repaso` ni los clasificadores. Este documento es el
  mapa completo.
- **El índice de `docs/README.md` apunta a `08-mejoras.md`**, pero el archivo real
  es `08-comandos-olt.md`.
- **Dos esquemas conviven en las consultas**: `olt_test.*`, hardcodeado en
  `queryRegistrosProvisioning` y `queryCargaPrincipal`, y tablas sin prefijo
  (`observacionesct`, `conexiones`, `onu`, `olt`) que caen en el `DB_NAME` del
  `.env`. El prefijo fijo ignora `DB_NAME`: si se apunta a otra base, esas
  consultas siguen leyendo `olt_test`.
- **`golang.org/x/crypto` sigue declarado en `go.mod` pero ya no se usa** — quedó
  de la versión que conectaba con la librería nativa de Go. `go mod tidy` lo saca.
- **Hay un binario `provisionar` de 8 MB en la raíz** que no debería versionarse.
- **`internal/database` mezcla dos responsabilidades**: acceso a datos y
  clasificación. Los dos `clasificador*.go` suman ~700 líneas sin una sola
  sentencia SQL.
