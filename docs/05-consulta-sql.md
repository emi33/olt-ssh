# 5. La consulta SQL

La consulta que genera los comandos vive en
[`internal/database/query.go`](../internal/database/query.go) como la constante
`queryComandosRegistro`, y se ejecuta desde
[`GetComandosRegistro()`](../internal/database/database.go). Por cada ONT del
puerto consultado produce **dos cadenas** listas para enviar a la OLT.

> ℹ️ La lógica es la misma que la de la versión PHP; ver también
> [05-consulta-sql.md (PHP)](../../docs/05-consulta-sql.md).

## Tablas implicadas

| Tabla | Rol |
|-------|-----|
| `olt.onu` (`t1`) | Las ONTs físicas registradas (puerto, identificador, SN, MAC). |
| `olt.eqcliente` (`t2`) | Datos de servicio/cliente, sobre todo la `vlan`. Se une con `LEFT JOIN` para que la ONT aparezca aunque **no** tenga servicio (en ese caso `vlan` es `NULL`). |

El `JOIN` empareja por OLT/MAC + puerto + identificador:

```sql
LEFT JOIN olt.eqcliente t2
    ON (t2.idOlt = t1.idOlt OR t2.mac = t1.mac)
    AND t2.puertoOlt = t1.puertoOlt
    AND t2.identificador = t1.identificador
```

## Las dos salidas

Cada fila devuelve dos columnas, que en Go se leen en `Comando.Comando1` y
`Comando.Comando2`:

```text
comando1 → service-port <N> config gpon 1/1/<puerto> ont <id> gem-id <gem>
           svlan <vlan> user-vlan <vlan> tag-action transparent
comando2 → ont add <id> sn-auth <SN-formateado> ont-lineprofile-id 1 ont-srvprofile-id 1
```

Ejemplo:

```text
comando2: ont add 1 sn-auth DF51-A63BCBD1 ont-lineprofile-id 1 ont-srvprofile-id 1
comando1: service-port 3698 config gpon 1/1/16 ont 1 gem-id 1 svlan 666 user-vlan 666 tag-action transparent
```

## El contador `@x`

Los `service-port` se numeran de forma **correlativa** con la variable de usuario
`@x`. Se inicializa en **3697** mediante un `CROSS JOIN`:

```sql
CROSS JOIN (SELECT @x := 3697) AS inicializador
```

y se incrementa **en la capa externa** del `SELECT`, ya numerando sobre el
resultado ya ordenado:

```sql
'service-port ', (@x := @x + 1), ...
```

Así el primer `service-port` queda en **3698**, el segundo en 3699, etc.

> ⚠️ **Por qué una conexión dedicada:** `@x` es una variable de *sesión*. Si la
> inicialización (`SELECT @x := 3697`) y la lectura se ejecutaran en conexiones
> distintas del pool de `database/sql`, el contador se perdería. Por eso
> `GetComandosRegistro` toma una conexión con `db.Conn(ctx)` y ejecuta todo sobre
> ella. En el PHP esto no hacía falta (una sola conexión PDO).

## El `gem-id` calculado

Un `CASE` traduce la VLAN del cliente a un número de GEM port. Se usa
`COALESCE(t2.vlan, 666)`: si no hay VLAN, se asume 666.

| VLAN del servicio | `gem-id` |
|-------------------|----------|
| 10, 20, 21, 30, 31, 50, 62, 72 | `1` |
| 102, 104 | `3` |
| 572 | `4` |
| 600, 620, 1001, 1062 | `2` |
| cualquier otra (incl. 666) | `8` (el `ELSE`) |

## La `svlan` calculada

```sql
COALESCE(
  GROUP_CONCAT(DISTINCT CASE WHEN t2.vlan NOT IN (1, 2, 80) THEN t2.vlan END
               ORDER BY t2.vlan SEPARATOR ', '),
  '666'
)
```

Junta todas las VLANs de la ONT en una lista separada por comas, **excluyendo**
las VLANs de gestión `1`, `2` y `80`. Si no queda ninguna válida, el `COALESCE`
pone `'666'`. Por eso una `svlan 666` indica que la ONT no tenía VLAN de servicio
válida en `eqcliente` (ver [07-logs-y-troubleshooting.md](07-logs-y-troubleshooting.md)).

El número de serie del `comando2` se formatea como `XXXX-resto`:

```sql
CONCAT(LEFT(t1.sn, 4), '-', SUBSTRING(t1.sn, 5))   -- DF51A63BCBD1 -> DF51-A63BCBD1
```

## Filtrado y agrupación

```sql
WHERE t1.idOlt = ? AND t1.puertoOlt = ?
GROUP BY t1.sn, t1.idOlt, t1.puertoOlt, t1.identificador
ORDER BY t1.idOlt, t1.puertoOlt, t1.identificador
```

- Los dos `?` son **parámetros posicionales**: primero `idOlt`, después
  `puertoOlt` (vienen de `QUERY_ID_OLT` y `QUERY_PUERTO_OLT`).
- El `GROUP BY` por ONT permite que una ONT con varias VLANs (varias filas en
  `eqcliente`) se condense en una sola línea (de ahí el `GROUP_CONCAT`).
- El `ORDER BY` garantiza que la numeración del contador salga en orden 1, 2, 3…

> ⚠️ **Diferencia con el PHP:** los placeholders con nombre (`:idOlt`, `:puertoOlt`)
> del PHP pasan a `?` posicionales, porque `go-sql-driver/mysql` no soporta
> parámetros con nombre. El orden es **(idOlt, puertoOlt)**.

## Estructura de la consulta

```text
SELECT  (capa externa)            ← arma comando1 y numera con @x := @x + 1
  FROM ( subconsulta 'temporal' ) ← agrupa, calcula gem-id/svlan, ordena
  CROSS JOIN (SELECT @x := 3697)  ← inicializa el contador
```
