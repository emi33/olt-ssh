# Comandos OLT (DS-P7001-16)

Comandos que el programa envía a la OLT TP-Link DS-P7001-16 vía SSH.

## El binario: `cmd/provisionar`

Lee `registro_onu` + `registro_cliente` (`listo_para_cargar = 1`) y **arma los comandos en Go**. Guarda con `Conn.SaveConfig()` (sección 4) y usa `show service-port` (sección 3c) para asignar el índice sin colisiones.

### Dos operaciones sobre el mismo conjunto

`provisionar` filtra una sola vez (`seleccionarPendientes`) y sobre ese conjunto ofrece dos operaciones:

- **default** → crea los `service-port` (datos de `registro_cliente`).
- **`--registrar-onu`** → da de alta las ONTs con `ont add` (datos de `registro_onu`).

Se emite **un `ont add` por ONT** y **un `service-port` por cada VLAN** de esa ONT, así que puede haber más service-ports que ONTs.

Una fila se omite (y queda registrada como tal) si no tiene `SN` o no tiene VLAN válida. El filtro por `listo_para_cargar` ya viene aplicado desde la BD; los estados de presencia que lo habilitan (`mac_verificada`, `mac_sin_verificar`, `macs_multiples`) se resuelven en el flujo de `cmd/cargar` / `cmd/repaso`.

---

## 1. Flujo de comandos

**`cmd/provisionar`** (default, service-port):
```
enable → configure → show service-port → service-port ... (x cliente) → end → copy running-config startup-config + y
```

**`cmd/provisionar --registrar-onu`** (`ont add`, agrupado por puerto):
```
enable → configure → [interface gpon 1/1/<p> → ont add ... (x cliente del puerto) → exit] (x puerto) → end → copy running-config startup-config + y
```

---

## 2. Alta de ONT — `ont add`

Válido **solo** dentro de `interface gpon`. Registra una ONT por su Serial Number.

```
ont add <ONU_ID> sn-auth <SN> ont-lineprofile-id 1 ont-srvprofile-id 1 desc <DESC>
```

Secuencia completa:
```
enable
configure
interface gpon 1/1/<PUERTO>
ont add <ONU_ID> sn-auth <SN> ont-lineprofile-id 1 ont-srvprofile-id 1 desc <DESC>
exit
end
copy running-config startup-config
y
```

Ejemplo:
```
ont add 8 sn-auth TPLG-F58E9CB8 ont-lineprofile-id 1 ont-srvprofile-id 1 desc 2660
```

| Parámetro | Qué es |
|-----------|--------|
| `<ONU_ID>` | ONU-ID dentro del puerto, entero `0-127`. Va **entre `add` y `sn-auth`** |
| `<SN>` | Serial con guión tras el 4º carácter: `TPLGF58E9CB8` → `TPLG-F58E9CB8` |
| `ont-lineprofile-id 1` / `ont-srvprofile-id 1` | Perfiles de línea y servicio |
| `desc <DESC>` | Descripción libre (va al final, después de los profiles) |

Notas:
- `ont <id> add` es inválido (`Error: Invalid parameter`). El orden es siempre `ont add <id> ...`.
- Si se omite `<ONU_ID>` (`ont add sn-auth ...`), la OLT autoasigna el ID.

---

## 3. Service-port

### 3a. Formato (con tráfico + desc)
```
service-port <N> config gpon 1/1/<PUERTO> ont <id> gem-id 1 svlan <vlan> user-vlan <vlan> tag-action transparent traffic-in <tc> traffic-out <tc> desc <desc>
```
Ejemplo: `service-port 3699 config gpon 1/1/11 ont 58 gem-id 1 svlan 40 user-vlan 40 tag-action transparent traffic-in 1 traffic-out 1 desc 2660`

| Parámetro | Qué es |
|-----------|--------|
| `<N>` | Índice, se asigna en vivo con `show service-port` (3c) |
| `gem-id` | Columna `gem_id` de `registro_cliente`; `1` si no viene |
| `svlan` / `user-vlan` | VLAN de servicio del cliente (misma en ambas, modo transparente) |
| `<tc>` | Código de velocidad según plan — ver `codigoTrafico()`, `cmd/provisionar/main.go` |
| `<desc>` | `nro_cliente`, o el pppoe si no existe |

> ⚠️ **Dos reglas que la OLT impone y que el código respeta:**
> - **Una sola VLAN por service-port.** `svlan 3996,4000` devuelve `Error: Invalid parameter`. Por eso se emite un comando por VLAN.
> - **`desc` nunca vacío.** Terminar el comando en `desc ` (sin valor) devuelve `Error: Missing parameter data` y tumba el comando entero. `sufijoDesc()` omite el `desc` completo cuando no hay descripción.

**Códigos de tráfico (`codigoTrafico`):**

| Plan | `<tc>` |
|------|--------|
| 100 / 200 / 300 MB Hogar | 1 / 2 / 3 |
| 100 / 200 / 300 MB Pyme | 4 / 5 / 6 |
| 2 / 6 / 8 / 10 MB (unificados a 20 MB) | 7 |
| No reconocido / sin plan | se omite |

### 3c. `show service-port` (asignación de índice)
```
show service-port
```
- Con guión. `show service port` (con espacio) es inválido.
- Se ejecuta desde `(config)#`.
- Solo importa la columna `Index`. `SiguienteIndicesLibres()` calcula `max(ocupados)+1` y devuelve índices consecutivos libres.
- `--dry-run` no conecta, así que muestra `service-port <auto> ...`.

---

## 4. Guardar config
```
end
copy running-config startup-config
y
```
**Importante:** `save` desde `(config)#` da `Error: Bad command`. Hay que hacer `end` primero (volver a `#`) y usar `copy running-config startup-config` (la OLT pide `y/n`, el programa responde `y`). Timeout extendido de 30s.

---

## 5. Detección de errores

Tras cada comando se busca (case-insensitive): `error, failure, failed, invalid, not found, already exists, conflict`. Si aparece, se registra el error pero no se aborta el proceso.

---

## 6. Notas

- Se ejecutan TODOS los `ont add` primero, luego TODOS los `service-port`.
- Puertos GPON: `1/1/1` a `1/1/16`.
- Prompts: usuario `>`, privilegiado `#`, config `(config)#`, GPON `(config-if-gpon)#`.
- SSH con algoritmos legacy (diffie-hellman-group1-sha1, ssh-dss, aes128-cbc) porque la OLT no soporta modernos.
- Comandos NO disponibles en este firmware: `auto-service-port`, `show gpon onu autofind`, `ont autofind enable`.
