# 6. Conexión SSH a la OLT

La conexión vive en [`internal/olt/connection.go`](../internal/olt/connection.go),
sobre `golang.org/x/crypto/ssh` (equivale a `src/OltConnection.php`, que usaba
phpseclib3).

## ¿Por qué un shell con PTY y no `session.Run`?

La consola de la OLT es **interactiva**: tiene prompts (`>`, `#`), pide
confirmaciones y mantiene "modos" (enable, config, interface). No basta con lanzar
un comando y leer su salida; hay que abrir un **shell** con un **pseudo-terminal
(PTY)** y dialogar con él enviando texto por `stdin` y leyendo de `stdout`.

`Connect()` hace exactamente eso:

1. Marca un PTY `vt100` de 80x40 con `ECHO` activado.
2. Lanza `session.Shell()`.
3. Arranca una **goroutine lectora** (`startReader`) que vuelca todo el `stdout`
   en un canal `readCh`.
4. Consume el prompt inicial con `readUntil(promptRe, timeout)`.

## Algoritmos legacy

Muchas OLTs antiguas no negocian los algoritmos modernos que `x/crypto` ofrece por
defecto, así que se fuerzan explícitamente los heredados (equivalente al
`setPreferredAlgorithms()` del PHP):

```go
Config: ssh.Config{
    KeyExchanges: []string{"diffie-hellman-group1-sha1", "diffie-hellman-group14-sha1"},
    Ciphers:      []string{"aes128-cbc", "aes256-cbc", "3des-cbc"},
},
HostKeyAlgorithms: []string{"ssh-rsa", "ssh-dss"},
```

Además, la verificación de host key está **desactivada**
(`ssh.InsecureIgnoreHostKey()`), igual que en el PHP, para no fallar con OLTs sin
host key estable. La autenticación es por **contraseña** (`OLT_PASSWORD`).

> ⚠️ **DSA retirado:** el soporte de host key `ssh-dss` (DSA) se ha ido eliminando
> en versiones recientes de `golang.org/x/crypto`. Si la OLT exige `ssh-dss` y la
> conexión falla en la negociación, revisa la versión de `golang.org/x/crypto` en
> [`go.mod`](../go.mod).

## Los modos de la consola

```text
login        prompt '>'   ──en──►   enable       prompt '#'
                                        │ configure
                                        ▼
                                     config       prompt '(config)#'
                                        │ interface gpon 1/1/<n>
                                        ▼
                                     interface    prompt '(config-if)#'
                                        │ exit  ──► vuelve a config
```

`EnableMode()` envía `en`; si la respuesta contiene `password`, manda
`OLT_ENABLE_PASSWORD`. Es **idempotente** (usa el flag `inEnable`, así que llamarlo
dos veces no hace nada la segunda). Los pasos `configure`, `interface gpon` y
`exit` los emite el orquestador en [`registrar.Run()`](../internal/registrar/registrar.go).

## `ExecuteCommand()` paso a paso

```go
func (c *Conn) executeCommand(command string, timeout time.Duration) (string, error)
```

1. Comprueba que la conexión está establecida (`stdin != nil`).
2. Escribe `command + "\r"` en `stdin` (la OLT espera **retorno de carro**, no
   `\n`).
3. Espera 100 ms para que la OLT empiece a responder.
4. `readUntil(promptRe, timeout)` acumula la salida hasta que aparece el prompt
   (`[>#]\s*$`) o vence el timeout (10 s por defecto).
5. Limpia las secuencias ANSI con `cleanANSI` y registra el par comando/respuesta
   en el log (si hay logger).

### `readUntil` y la goroutine lectora

`startReader` lee el `stdout` en fragmentos de 4096 bytes y los empuja a un canal
con buffer de 32. `readUntil` consume ese canal acumulando en un buffer y devuelve
cuando:

- el regex casa (encontró el prompt), **o**
- vence el `deadline` (timeout) — en cuyo caso devuelve **lo que haya leído, sin
  error**, para no abortar el flujo (imita el modo `READ_REGEX` de phpseclib), **o**
- el canal se cierra (la conexión terminó).

### Limpieza de ANSI

La OLT envía códigos de color y movimiento de cursor. Se eliminan con dos regex:

```go
ansiRe1 = `\x1b\[[0-9;]*[a-zA-Z]`   // colores y secuencias genéricas
ansiRe2 = `\x1b\[\d+D`              // movimiento de cursor a la izquierda
```

## El "eco" del terminal

Como el PTY tiene `ECHO` activado, la propia OLT devuelve el comando que se le
envió; por eso en las respuestas del log suele aparecer el comando repetido antes
de su salida real. Es esperado.

## Guardado de configuración

`SaveConfig()` ejecuta `save` con un timeout ampliado a **30 s** (puede tardar) y,
como algunas OLTs piden confirmación `(y/n)`, envía `y` y espera el prompt `#`.

## Buenas prácticas y peculiaridades

- **Timeouts:** si ves respuestas truncadas, la OLT tardó más que el timeout de
  lectura; conviene revisar `OLT_TIMEOUT` y los 10 s por defecto de
  `ExecuteCommand` (ver [07-logs-y-troubleshooting.md](07-logs-y-troubleshooting.md)).
- **Desconexión garantizada:** el orquestador llama a `Disconnect()` con `defer`,
  de modo que la sesión SSH siempre se cierra, incluso ante un fallo.
- **`\r` y no `\n`:** todos los envíos a la OLT terminan en `\r`.
