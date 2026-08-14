# 10. Concurrencia y sistemas distribuidos

Documento de estudio. Explica los conceptos usando **código real de este
proyecto** — el que está bien y el que tiene bugs. Asume Go básico (structs,
interfaces, `go func`, slices) y busca llevarte al nivel siguiente.

Todos los ejemplos salen de `olt-ssh-go`. Cuando el código actual tiene un
problema, se dice cuál y cómo se arregla.

**Índice**

1. [Goroutines y canales: la mecánica](#1-goroutines-y-canales-la-mecánica)
2. [El patrón completo: `spinner.go`](#2-el-patrón-completo-spinnergo)
3. [El patrón difícil: leer un proceso externo](#3-el-patrón-difícil-leer-un-proceso-externo)
4. [Un bug de tipos disfrazado de bug de concurrencia](#4-un-bug-de-tipos-disfrazado-de-bug-de-concurrencia)
5. [`drain()`: `select` no bloqueante](#5-drain-select-no-bloqueante)
6. [Fugas de goroutines](#6-fugas-de-goroutines)
7. [Por qué la OLT no se puede paralelizar](#7-por-qué-la-olt-no-se-puede-paralelizar)
8. [Colas de mensajes](#8-colas-de-mensajes)
9. [Transacciones: lo que ya tenés gratis](#9-transacciones-lo-que-ya-tenés-gratis)
10. [Estado compartido: la parte distribuida que ya existe](#10-estado-compartido-la-parte-distribuida-que-ya-existe)
11. [Go idiomático en este proyecto](#11-go-idiomático-en-este-proyecto)
12. [Ejercicios](#12-ejercicios)

---

## 1. Goroutines y canales: la mecánica

### 1.1 Goroutine

Una goroutine es una función que corre concurrentemente, gestionada por el runtime
de Go y no por el sistema operativo. Arranca con ~8 KB de stack (un hilo del SO
arranca con 1–8 MB) y crece si hace falta. Podés tener cientos de miles.

```go
go s.run()   // arranca y sigue de largo, no espera
```

Detalle importante: **`main` no espera a las goroutines**. Si `main` retorna, el
proceso termina y todo lo que estaba corriendo se corta a la mitad. Por eso hace
falta un mecanismo explícito de espera (`WaitGroup`, o un canal).

### 1.2 Canal

Un canal es una cola tipada con sincronización incorporada. Su comportamiento
depende de tres estados, y conviene tenerlos memorizados porque explican casi
todos los bugs de concurrencia:

| Estado del canal | Enviar (`ch <- v`) | Recibir (`<-ch`) |
|---|---|---|
| **nil** (declarado sin `make`) | bloquea **para siempre** | bloquea **para siempre** |
| **abierto, con lugar** | ok | ok |
| **abierto, sin lugar** | bloquea hasta que alguien lea | bloquea hasta que alguien escriba |
| **cerrado** | **panic** | devuelve el valor cero, con `ok == false` |

Tres consecuencias prácticas:

- **Cerrar es cosa del que escribe, nunca del que lee.** Si el lector cierra, el
  escritor entra en panic la próxima vez que envíe.
- **Cerrar dos veces es panic.** Si hay varios que podrían cerrar, hace falta un
  `sync.Once` o rediseñar.
- **Un canal nil en un `select` desactiva ese caso.** Es un truco idiomático: si
  ponés `ch = nil`, la rama que lo lee deja de participar y el `select` sigue
  atendiendo las otras.

### 1.3 Buffereado vs sin buffer

```go
make(chan []byte)       // sin buffer: el envío espera al receptor
make(chan []byte, 32)   // con buffer: 32 envíos antes de bloquear
```

Sin buffer, el envío y la recepción son un **encuentro**: los dos lados se esperan.
Con buffer, el que escribe puede adelantarse hasta llenar el espacio.

La regla práctica: el buffer sirve para absorber **ráfagas**, no para evitar
bloqueos. Si el consumidor es sistemáticamente más lento que el productor, el
buffer se llena igual y el bloqueo aparece — sólo que más tarde y de forma menos
predecible. En la sección 6 vamos a ver exactamente ese caso.

### 1.4 `select`

`select` espera en varios canales simultáneamente.

```go
select {
case v := <-ch1:     // si ch1 tiene algo
case ch2 <- x:       // si ch2 acepta
case <-time.After(d):// timeout
default:             // si NINGUNO está listo AHORA (hace el select no bloqueante)
}
```

Dos reglas que sorprenden:

- Si **varios** casos están listos, Go elige **uno al azar**, con distribución
  uniforme. No es en orden de escritura. Es deliberado, para que un canal muy
  activo no deje sin atención a los demás.
- Con `default`, `select` **nunca bloquea**: si no hay nada listo en ese instante,
  ejecuta el `default` y sigue.

### 1.5 La garantía de memoria

¿Por qué es seguro mandar un puntero por un canal, si dos goroutines lo van a
tocar? Por el *memory model* de Go:

> Un envío a un canal ocurre-antes (*happens-before*) de que la recepción
> correspondiente se complete.

Eso significa que todo lo que la goroutine emisora escribió **antes** del envío es
visible para la receptora **después** de recibir. El canal no sólo transporta el
dato: publica también todo lo escrito antes. Es lo que hace innecesario un mutex
cuando se pasa propiedad de un dato por canal.

De ahí el lema de Go:

> *Don't communicate by sharing memory; share memory by communicating.*

---

## 2. El patrón completo: `spinner.go`

`internal/spinner/spinner.go` tiene 53 líneas y es un ejemplo de manual. Vale la
pena desarmarlo entero.

```go
type Spinner struct {
    mensaje string
    stop    chan struct{}   // (1)
    wg      sync.WaitGroup  // (2)
}

func Start(mensaje string) *Spinner {
    s := &Spinner{mensaje: mensaje, stop: make(chan struct{})}
    s.wg.Add(1)             // (3) ANTES del go
    go s.run()
    return s
}

func (s *Spinner) run() {
    defer s.wg.Done()       // (4)
    frames := []rune{'⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏'}
    t := time.NewTicker(90 * time.Millisecond)
    defer t.Stop()          // (5)
    i := 0
    for {
        select {
        case <-s.stop:      // (6)
            fmt.Printf("\r\033[K%s ✓\n", s.mensaje)
            return
        case <-t.C:
            fmt.Printf("\r\033[K%s %c", s.mensaje, frames[i%len(frames)])
            i++
        }
    }
}

func (s *Spinner) Stop() {
    close(s.stop)           // (7)
    s.wg.Wait()             // (8)
}
```

**(1) `chan struct{}`** — `struct{}` es el tipo vacío: ocupa **cero bytes**. Cuando
el canal sólo transporta *el hecho de que algo pasó*, y no un dato, es el tipo
idiomático. `chan bool` funcionaría, pero sugiere falsamente que el valor importa.

**(2) `sync.WaitGroup`** — un contador con espera. No se copia nunca: si pasás un
`Spinner` por valor, copiás el WaitGroup y el `Wait` mira un contador distinto al
que toca el `Done`. Por eso todos los métodos usan receptor puntero (`*Spinner`).

**(3) `Add(1)` va antes del `go`** — no adentro de la goroutine. Si estuviera
adentro, habría una carrera: `Stop()` podría llamar a `Wait()` con el contador
todavía en 0 y volver inmediatamente, sin esperar nada.

**(4) `defer s.wg.Done()`** — garantiza el decremento por **cualquier** camino de
salida, incluido un panic. Sin `defer`, un `return` temprano dejaría el contador
en 1 y `Wait()` colgaría el programa para siempre.

**(5) `defer t.Stop()`** — un `Ticker` mantiene un timer vivo en el runtime hasta
que lo pares. Sin el `Stop()`, cada spinner creado dejaría un ticker corriendo:
una fuga silenciosa de recursos.

**(6) `close(s.stop)` como broadcast** — cerrar desbloquea a **todos** los lectores
y para siempre. Mandar un valor (`s.stop <- struct{}{}`) despertaría a uno solo.
Por eso la señal de "apagate" se implementa cerrando, no enviando.

**(7) + (8) apagado ordenado** — `close` pide que pare, `Wait` espera a que
efectivamente haya parado. Sin el `Wait`, `Stop()` volvería enseguida y la
siguiente línea del programa podría imprimirse **antes** de que el spinner limpie
la terminal, dejando la salida pisada.

---

## 3. El patrón difícil: leer un proceso externo

`internal/olt/connection.go` resuelve algo más complicado. La conexión con la OLT
no usa una librería SSH de Go: lanza `sshpass` + `ssh` como **proceso hijo** y
habla con él por stdin/stdout.

```go
func (c *Conn) startReader(stdout io.Reader) {
    c.readCh = make(chan []byte, 32)
    go func() {
        buf := make([]byte, 4096)
        for {
            n, err := stdout.Read(buf)
            if n > 0 {
                chunk := make([]byte, n)
                copy(chunk, buf[:n])     // (A)
                c.readCh <- chunk
            }
            if err != nil {
                close(c.readCh)          // (B)
                return
            }
        }
    }()
}
```

### 3.1 Por qué hace falta una goroutine

Porque `stdout.Read()` es **bloqueante y no cancelable**. Si la OLT no responde
nunca, ese `Read` se cuelga para siempre: no hay timeout, no hay forma de
interrumpirlo desde afuera.

Metiéndolo en una goroutine que publica a un canal, el consumidor puede usar
`select` con timeout y **seguir adelante**. La goroutine queda colgada en el
`Read`, sí — pero el programa no.

Éste es el patrón general que conviene grabarse:

> **Para poder ponerle timeout a una operación bloqueante, envolvela en una
> goroutine que publique el resultado en un canal, y esperá con `select`.**

### 3.2 (A) El `copy` no es opcional

Un slice en Go es una estructura de tres campos: **puntero al array, longitud,
capacidad**. Asignar un slice copia esos tres campos, **no el array**.

`buf` se reutiliza en cada vuelta del `for`. Si enviaras `buf[:n]` directo al
canal, estarías mandando un slice que apunta **al mismo array** que la próxima
llamada a `Read` va a sobrescribir. El receptor terminaría leyendo datos de otro
chunk — corrupción silenciosa, dependiente del timing, imposible de reproducir.

`copy(chunk, buf[:n])` a un array nuevo lo evita. Es uno de los errores más
frecuentes con slices en Go.

### 3.3 (B) Quién cierra

La goroutine lectora es la **única** que escribe en `readCh`, y por eso es la única
que lo cierra. Respeta la regla de 1.2. El consumidor detecta el cierre con el
`comma ok`:

```go
case chunk, ok := <-c.readCh:
    if !ok {
        // canal cerrado: el proceso hijo terminó
    }
```

### 3.4 El buffer de 32

`make(chan []byte, 32)` permite acumular hasta 32 chunks sin que nadie los retire.
Sin buffer, cada `Read` del proceso hijo esperaría al consumidor. Con buffer, se
absorben ráfagas — típico cuando la OLT vuelca un `show` largo de golpe.

Guardá esta idea: en la sección 6 vamos a ver que ese mismo buffer **esconde** un
bug hasta el chunk 33.

### 3.5 Dos detalles de `readUntil` que están bien

```go
func (c *Conn) readUntil(re *regexp.Regexp, timeout time.Duration) string {
    var buf bytes.Buffer
    deadline := time.After(timeout)   // (C) FUERA del for
    for {
        select {
        case chunk, ok := <-c.readCh:
            buf.Write(chunk)
            if re.Match(buf.Bytes()) { ... }   // (D)
        case <-deadline:
            ...
        }
    }
}
```

**(C)** `time.After` está **fuera** del bucle, y eso es correcto por dos motivos.
El primero es semántico: así el timeout es absoluto para toda la llamada. Si
estuviera adentro del `for`, se reiniciaría con cada chunk y una OLT que manda
datos lentamente sin llegar nunca al prompt no vencería jamás.

El segundo es de recursos: **`time.After` crea un `Timer` que el recolector de
basura no puede liberar hasta que dispare**. Llamarlo dentro de un bucle muy
activo va acumulando timers vivos. Es una fuga clásica de Go. (Si necesitás
reiniciar el plazo, usá `time.NewTimer` + `Reset` y `defer t.Stop()`.)

**(D)** Acá hay un costo escondido: `re.Match(buf.Bytes())` reescanea el **buffer
completo** cada vez que llega un chunk. Con N chunks eso es O(N²) sobre el tamaño
de la respuesta. Para un comando corto da igual; para un `show service-port` con
1000+ líneas es trabajo real y evitable — alcanzaría con buscar el prompt sólo en
la cola del buffer.

---

## 4. Un bug de tipos disfrazado de bug de concurrencia

Ahora la parte importante. Mirá las salidas de `readUntil`:

```go
func (c *Conn) readUntil(re *regexp.Regexp, timeout time.Duration) string {
    var buf bytes.Buffer
    deadline := time.After(timeout)
    for {
        select {
        case chunk, ok := <-c.readCh:
            if !ok {
                return buf.String()          // (A) el proceso murió
            }
            buf.Write(chunk)
            if re.Match(buf.Bytes()) {
                return buf.String()          // (B) ¡éxito! apareció el prompt
            }
        case <-deadline:
            return buf.String()              // (C) se acabó el tiempo
        }
    }
}
```

Hay **tres salidas con significados incompatibles** —el comando terminó, la
conexión se cayó, venció el timeout— y las tres devuelven exactamente lo mismo: un
`string`.

La firma `func(...) string` **no puede expresar la diferencia**. La información
existe adentro de la función y se destruye al retornar.

### 4.1 La consecuencia

```go
func (c *Conn) executeCommand(command string, timeout time.Duration) (string, error) {
    // ...
    raw := c.readUntil(promptRe, timeout)
    clean := cleanANSI(raw)
    // ...
    return clean, nil        // ← err es SIEMPRE nil
}
```

Y arriba, en `cmd/provisionar`:

```go
resp, err := conn.ExecuteCommand(comando)
if err != nil { /* nunca entra por timeout */ }
if detectaError(resp) {
    // busca "error", "failed", "invalid"... en la salida
} else {
    ejecutados++             // ← cuenta como OK
}
```

Encadenado: un `service-port` que hizo timeout devuelve salida **parcial** con
`err == nil`; esa salida parcial no contiene la palabra "error"; el llamador lo
cuenta como ejecutado; el resumen final imprime **"Estado: ÉXITO"**. La OLT quedó
sin configurar y nadie se entera.

Sobre equipos de red en producción, eso es serio.

### 4.2 Cómo se arregla

En Go, cuando una función puede terminar de varias maneras, **eso va en el tipo de
retorno**. Hay dos formas idiomáticas:

```go
// Opción 1: comma-ok. Alcanza si sólo importa "¿funcionó?"
func (c *Conn) readUntil(re *regexp.Regexp, timeout time.Duration) (string, bool)

// Opción 2: error. Si al llamador le sirve saber POR QUÉ falló.
func (c *Conn) readUntil(re *regexp.Regexp, timeout time.Duration) (string, error)
```

Con la segunda, quedaría así:

```go
case chunk, ok := <-c.readCh:
    if !ok {
        return buf.String(), errors.New("la conexión se cerró inesperadamente")
    }
    buf.Write(chunk)
    if re.Match(buf.Bytes()) {
        return buf.String(), nil                     // único camino exitoso
    }
case <-deadline:
    return buf.String(), fmt.Errorf("timeout de %s esperando el prompt", timeout)
```

Fijate que **se devuelve el buffer igual en los casos de error**. Eso es
deliberado: la salida parcial sirve para diagnosticar. El error dice "no confíes en
esto"; el string dice "esto es lo que alcancé a ver".

Y `executeCommand` propaga:

```go
raw, err := c.readUntil(promptRe, timeout)
if err != nil {
    return cleanANSI(raw), fmt.Errorf("ejecutando %q: %w", command, err)
}
```

### 4.3 Por qué es una lección de diseño y no un descuido

El bug no está en la concurrencia: el `select`, el canal y el timeout están todos
bien implementados. Está en que **el tipo de retorno perdió información que la
función sí tenía**.

Es un caso concreto de un principio general: *hacé que los estados imposibles sean
irrepresentables, y que los estados posibles sean visibles en la firma.* Si una
función puede fallar, su firma tiene que decirlo. Si no lo dice, todos los que la
llaman van a asumir que siempre funciona — y van a tener razón, hasta que no.

---

## 5. `drain()`: `select` no bloqueante

```go
func (c *Conn) drain() {
    for {
        select {
        case _, ok := <-c.readCh:
            if !ok {
                return            // canal cerrado
            }
            // descarta el chunk y sigue
        default:
            return                // no hay nada más: salgo
        }
    }
}
```

El `default` es lo que hace este `select` **no bloqueante**. Sin él, el `for`
quedaría esperando el próximo chunk para siempre.

El patrón "vaciar un canal sin bloquear" es exactamente éste: `for` + `select` con
`default`.

**Por qué hace falta acá:** antes de mandar un comando nuevo hay que descartar lo
que haya quedado del anterior (ecos, restos de prompt). Si no, `readUntil` podría
encontrar el prompt **viejo** en el buffer y devolver enseguida, sin haber esperado
la respuesta real. Sería un falso positivo.

**Una inconsistencia real del código:** `executeCommand` llama a `drain()`, pero
`executeCommandPaginado` (en `internal/olt/serviceport.go`) **no**, y además
conserva un `time.Sleep(100 * time.Millisecond)` que `executeCommand` eliminó a
propósito. Dos rutas que hacen lo mismo con criterios distintos: la clase de
divergencia que produce bugs que aparecen sólo en una de las dos.

---

## 6. Fugas de goroutines

Una goroutine bloqueada para siempre en un canal que nadie va a leer **nunca se
libera**. El recolector de basura no la puede recuperar: no es basura, está viva y
esperando. Eso es una fuga.

Mirá el camino de reconexión:

```go
func (c *Conn) Connect() error {
    for intento := 1; intento <= attempts; intento++ {
        if err := c.connectOnce(); err == nil { return nil }
        c.cleanup()
        time.Sleep(c.cfg.RetryDelay)
    }
    // ...
}

func (c *Conn) cleanup() {
    if c.stdin != nil { c.stdin.Close(); c.stdin = nil }
    if c.cmd != nil && c.cmd.Process != nil {
        c.cmd.Process.Kill()
        c.cmd.Wait()
        c.cmd = nil
    }
    c.readCh = nil        // ← acá está el problema
}
```

`c.readCh = nil` cambia **el campo del struct**, pero la goroutine lectora capturó
el canal por clausura y **tiene su propia referencia al canal viejo**. Poner el
campo en nil no la afecta en absoluto.

Si esa goroutine está bloqueada en `c.readCh <- chunk` y ya nadie lee ese canal,
queda bloqueada **para siempre**.

### 6.1 Por qué el buffer lo esconde

La fuga no se dispara enseguida. Los primeros 32 chunks entran al buffer sin
bloquear. Recién el 33 cuelga la goroutine.

O sea: un bug que aparece **sólo** cuando la OLT es habladora **y** hay un
reintento de conexión. Casi nunca, y nunca igual dos veces. Los buffers no
previenen las fugas: las hacen intermitentes, que es peor.

### 6.2 Cómo se hace bien

La goroutine tiene que poder salir. El patrón es un segundo canal en el `select`
del envío:

```go
func (c *Conn) startReader(stdout io.Reader, done <-chan struct{}) {
    ch := make(chan []byte, 32)
    c.readCh = ch
    go func() {
        defer close(ch)
        buf := make([]byte, 4096)
        for {
            n, err := stdout.Read(buf)
            if n > 0 {
                chunk := make([]byte, n)
                copy(chunk, buf[:n])
                select {
                case ch <- chunk:      // camino normal
                case <-done:           // alguien canceló: salgo
                    return
                }
            }
            if err != nil { return }
        }
    }()
}
```

Y `cleanup()` cierra `done` en vez de poner el campo en nil.

La versión moderna del mismo patrón usa `context.Context`
(`case <-ctx.Done():`), que además propaga la cancelación por toda la pila de
llamadas.

### 6.3 Cómo se detectan

```bash
go test -race ./...     # detector de carreras de datos
go vet ./...            # errores comunes (incluye mal uso de canales)
```

Para fugas específicamente existe `go.uber.org/goleak`, que en el `TestMain`
verifica que al terminar los tests no quede ninguna goroutine viva:

```go
func TestMain(m *testing.M) {
    goleak.VerifyTestMain(m)
}
```

Y en un proceso corriendo, `runtime.NumGoroutine()` o el endpoint
`/debug/pprof/goroutine` muestran cuántas hay y dónde están bloqueadas.

---

## 7. Por qué la OLT no se puede paralelizar

Pregunta natural: si Go hace la concurrencia tan barata, ¿por qué `cmd/provisionar`
no manda los 500 comandos en 500 goroutines?

### 7.1 Porque la OLT es una máquina de estados modal

```
login   ──en──►   privilegiado   ──configure──►   config   ──interface gpon 1/1/3──►   interfaz
  >                    #                        (config)#                          (config-if)#
```

Los comandos **sólo son válidos en un modo**. `ont add` existe únicamente dentro de
`interface gpon 1/1/<puerto>`; `service-port` sólo en `(config)#`. Por eso
`ejecutarRegistroOnu` agrupa las ONTs por puerto: entra a la interfaz una vez,
ejecuta todas las de ese puerto, y sale con `exit`.

Además hay **una sola sesión SSH**: un `stdin`, un `stdout`, un estado. Dos
goroutines escribiendo en ese `stdin` intercalarían comandos, y cada una vería el
modo que dejó la otra. Un `ont add` podría ejecutarse en el puerto equivocado.

### 7.2 Un mutex no arregla esto

Podrías proteger la sesión con un `sync.Mutex`. Pero entonces cada goroutine
esperaría el turno de la anterior: los comandos se ejecutarían **igual de
secuencialmente**, con más código y más formas de equivocarse.

> Un mutex no crea paralelismo. Sólo hace seguro el que ya existe.

El recurso —la sesión con estado— es intrínsecamente único. Ninguna primitiva de
concurrencia lo cambia.

### 7.3 Dónde sí habría paralelismo real

Entre **OLTs distintas**: cada una es una sesión independiente. Ahí el patrón
idiomático es un **semáforo con canal buffereado**:

```go
var wg sync.WaitGroup
sem := make(chan struct{}, 4)     // como mucho 4 OLTs en paralelo

for _, olt := range olts {
    wg.Add(1)
    go func(o OLT) {              // (!) el parámetro, no la variable del for
        defer wg.Done()
        sem <- struct{}{}         // tomo un permiso (bloquea si hay 4 activas)
        defer func() { <-sem }()  // lo devuelvo al salir
        procesar(o)
    }(olt)
}
wg.Wait()
```

El buffer del canal **es** el límite de concurrencia. No hace falta ninguna
librería.

> **Nota sobre el `(olt)` del final**: en Go anteriores a 1.22, la variable del
> `for` se reutilizaba en cada vuelta y todas las goroutines veían la última. Por
> eso se pasaba como parámetro. Desde Go 1.22 cada iteración tiene su propia
> variable y ya no hace falta — pero vas a ver el patrón en todo el código
> existente, y pasarla explícitamente sigue siendo más claro.

---

## 8. Colas de mensajes

Una cola desacopla al productor del consumidor en el tiempo: uno deja tareas, otro
las levanta cuando puede.

### 8.1 Garantías de entrega

Lo que de verdad define a una cola son sus garantías:

| Garantía | Significa | Costo |
|---|---|---|
| *at-most-once* | se entrega 0 o 1 vez; **se puede perder** | ninguno |
| *at-least-once* | nunca se pierde, pero **puede duplicarse** | el consumidor debe ser idempotente |
| *exactly-once* | exactamente una vez | carísimo, y casi siempre exagerado por el marketing |

Prácticamente todas las colas reales dan **at-least-once**. El mecanismo: cuando un
consumidor toma un mensaje, éste no se borra — queda *invisible* durante un lapso
(*visibility timeout*). Si el consumidor confirma (`ack`), se borra. Si se cae
antes de confirmar, el mensaje **reaparece y otro lo procesa de nuevo**.

De ahí la regla de oro:

> Con at-least-once, procesar el mismo mensaje dos veces tiene que dar el mismo
> resultado que procesarlo una. Eso es **idempotencia**.

Los mensajes que fallan repetidamente terminan en una *dead-letter queue*, para que
un humano los mire en vez de reintentar para siempre.

### 8.2 Este proyecto no es idempotente

Acá está el punto técnico interesante. Así se arma un comando en `cmd/provisionar`:

```go
ocupados, err := conn.IndicesServicePortOcupados()          // consulta la OLT EN VIVO
indices := olt.SiguienteIndicesLibres(ocupados, len(pendientes), spInicio)
for i := range pendientes {
    pendientes[i].spNum = indices[i]
}
comando := fmt.Sprintf("service-port %d %s", e.spNum, e.sufijoSP)
```

El índice **no está en el mensaje**: se calcula en memoria a partir del estado
actual de la OLT (`max(ocupados)+1`). Si hubiera un reintento:

1. El primer intento creó el `service-port 3699` → ahora 3699 está ocupado.
2. El reintento vuelve a consultar, ve 3699 ocupado, y asigna **3700**.
3. Resultado: **dos service-ports para el mismo cliente**.

El comando no es idempotente, y meterlo en una cola at-least-once produciría
duplicados silenciosos en el equipo.

### 8.3 Qué haría falta para arreglarlo

Nada imposible, pero son cambios de diseño reales:

- **Clave natural en vez de contador**: derivar el índice de algo estable del
  cliente (mac, o puerto+ont), no de `max(ocupados)+1`.
- **Verificar antes de crear**: consultar si ya existe un service-port para ese
  cliente y no hacer nada si está.
- **Registro de ejecutados**: guardar el id del mensaje ya procesado y descartar
  repetidos.

> **La lección**: una cola no es sólo infraestructura que agregás al costado.
> **Impone requisitos al código que consume.** Hoy el bucle secuencial no los
> necesita porque no hay reintentos automáticos.

---

## 9. Transacciones: lo que ya tenés gratis

### 9.1 ACID

Una transacción de base de datos garantiza cuatro cosas:

- **Atomicidad** — o pasan todas las operaciones, o ninguna.
- **Consistencia** — las restricciones (FK, unique) se respetan al terminar.
- **Aislamiento** — las transacciones concurrentes no se ven a medio hacer.
- **Durabilidad** — una vez confirmada, sobrevive a un corte de luz.

### 9.2 `ReemplazarRegistros`, línea por línea

En `internal/database/database.go`:

```go
func (d *DB) ReemplazarRegistros(ctx context.Context, idOlt int, onus []RegistroOnu,
    clientes []RegistroCliente, rellenar bool) (ResultadoEscritura, error) {

    tx, err := d.db.BeginTx(ctx, nil)
    if err != nil { return res, fmt.Errorf("abriendo transacción: %w", err) }
    defer tx.Rollback()                                  // (1)

    tx.ExecContext(ctx, "DELETE FROM registro_cliente WHERE id_olt = ?", idOlt)  // (2)
    tx.ExecContext(ctx, "DELETE FROM registro_onu     WHERE id_olt = ?", idOlt)

    stmtOnu, _ := tx.PrepareContext(ctx, insertRegistroOnu)   // (3)
    defer stmtOnu.Close()
    for _, r := range onus { stmtOnu.ExecContext(ctx, ...) }

    // ... lo mismo para clientes ...

    return res, tx.Commit()                              // (4)
}
```

**(1) `defer tx.Rollback()`** — el idioma central. Cubre **todos** los caminos de
salida (error temprano, panic, o el feliz) sin repetirlo en cada `return`. Tras un
`Commit` exitoso, `Rollback` devuelve `sql.ErrTxDone` y no hace nada: es un no-op
seguro. Sin esto, un `return` por error dejaría la transacción abierta y la
conexión trabada hasta que el pool la reciclara.

**(2) El orden de borrado no es arbitrario.** La restricción
`fk_regcliente_regonu` obliga a que no exista un cliente huérfano, así que hay que
borrar **clientes antes que ONUs** — y al insertar, **ONUs antes que clientes**. La
base impone el orden; el código lo respeta.

**(3) `PrepareContext`** — la sentencia se compila **una vez** y se ejecuta N
veces. Con 1300 filas, la diferencia contra armar el SQL en cada vuelta es
grande. Y `defer stmt.Close()` libera el recurso del lado del servidor.

**(4) `tx.Commit()`** — recién acá se hace visible para los demás. Antes de esa
línea, nadie ve nada.

**Por qué importa que sea una sola transacción:** el flujo *borra todo y reescribe*.
Si se cortara entre el DELETE y los INSERT sin transacción, las tablas quedarían
**vacías**. Con transacción, un corte a mitad revierte solo: o entra todo lo nuevo,
o queda todo lo viejo. Nunca el estado intermedio.

Son diez líneas y es correcto por construcción. Esto es lo que se pierde al
repartir el sistema.

### 9.3 Qué costaría repartirlo

Imaginá "servicio de ONUs" y "servicio de clientes", cada uno con su base. Ya no
existe una transacción que abarque las dos. Necesitarías:

- **2PC (two-phase commit)** — un coordinador pregunta "¿pueden confirmar?", todos
  responden, y después ordena "confirmen". Es **bloqueante**: si el coordinador se
  cae entre las dos fases, los participantes quedan con los recursos trabados,
  esperando una orden que no llega. Casi nadie lo usa en sistemas nuevos.
- **Saga** — una secuencia de transacciones locales, cada una con su **acción
  compensatoria**. Si el paso 3 falla, ejecutás las compensaciones de 2 y 1. O sea:
  escribís a mano el rollback que la base te daba gratis. Y las compensaciones
  también pueden fallar, así que necesitan reintentos, y por lo tanto ser
  idempotentes (volvemos a la sección 8).

Encima, el orden que impone la FK deja de ser dos líneas seguidas y pasa a ser un
protocolo de coordinación entre servicios.

> Los microservicios se justifican cuando el costo **organizacional** de un
> monolito (equipos que se pisan al deployar) supera este costo **técnico**. Con un
> equipo chico y corridas batch, el intercambio es pésimo.

---

## 10. Estado compartido: la parte distribuida que ya existe

Éste es el punto que más se pasa por alto: **no hace falta elegir microservicios
para tener los problemas de los sistemas distribuidos.** Alcanza con que dos
procesos compartan estado.

### 10.1 Este proyecto lee tablas que no escribe

De las tablas que usa `olt-ssh-go`, sólo escribe dos: `registro_onu` y
`registro_cliente`. Las demás llegan pobladas desde afuera:

| Tabla | Quién la escribe | Qué aporta |
|---|---|---|
| `onu` | proceso externo de lectura de la OLT | inventario de ONUs, `estadoPresencia` |
| `eqcliente` | proceso externo de lectura de la OLT | histórico de MACs por posición |
| `observacionesct` | los concentradores CT | mac → cliente |
| `conexiones` | sistema de gestión de clientes | plan, nro de conexión |
| `planes`, `gem_config` | configuración manual | tablas de referencia |

Esa base **es la API** entre este proyecto y los que la alimentan. Y no hay
contrato explícito, ni versionado, ni una noción compartida de "corrida completa".

### 10.2 La carrera concreta

`cmd/repaso` lee `eqcliente` entera:

```sql
SELECT *, ROW_NUMBER() OVER (PARTITION BY mac, vlan ORDER BY fechaHora DESC) AS rn
FROM olt_test.eqcliente WHERE idOlt = ?
```

Si el proceso que **escribe** `eqcliente` está corriendo en ese momento, el repaso
ve una **foto a medio escribir**: algunas posiciones actualizadas, otras con datos
de la corrida anterior.

No hay error. No hay excepción. Sale un resultado plausible y equivocado. Es el
modo de falla más traicionero que existe: el sistema te miente con confianza.

### 10.3 Por qué el aislamiento de la base no alcanza

Podrías pensar que los niveles de aislamiento resuelven esto. InnoDB usa
`REPEATABLE READ` por defecto, que dentro de una transacción te da una foto
consistente del instante en que empezó.

Pero eso **no ayuda acá**, porque el problema no es ver escrituras a medias: es que
la corrida del escritor **todavía no terminó**. La mitad de las filas nuevas
simplemente **no existe** aún. Una foto perfectamente consistente de un estado
incompleto sigue siendo un estado incompleto.

Los niveles de aislamiento resuelven la concurrencia **dentro** de la base. No
resuelven la coordinación **entre procesos**.

### 10.4 El síntoma que ya se ve

`GetMacsHistoricasRepaso` no tiene corte de fecha sobre `eqcliente`: toma `rn=1`
por `(mac, vlan)` sobre **todo** el histórico. Un servicio que dejó de aparecer
queda en `registro_cliente` para siempre.

En una comparación real contra la última lectura de la OLT aparecieron **9 filas
de más**, todas con `fecha_eq` de corridas anteriores: MACs que existían a las
11:02 y ya no a las 12:42. No son un bug del matcheo —ninguna reclama un cliente
ajeno— pero se acumulan indefinidamente y pueden alterar la clasificación de una
ONU (una posición se clasificó sobre 20 servicios, de los cuales 8 ya no existían).

### 10.5 El contrato que falta

La solución no es tecnología nueva. Es **acordar qué significa "los datos están
completos"**: una marca de corrida (por ejemplo, un timestamp de ejecución
publicado sólo cuando la lectura terminó bien) que el repaso pueda usar para
filtrar, en vez de leer lo que haya en la tabla en ese instante.

Ese mismo marcador resolvería las dos cosas: la carrera **y** los servicios
fantasma.

> **La lección**: cuando dos procesos comparten una base, esa base es la interfaz
> entre ellos. Merece la misma disciplina que un endpoint HTTP — contrato,
> versionado, y una definición explícita de estado completo.

---

## 11. Go idiomático en este proyecto

### 11.1 Interfaces para testear — el patrón que el proyecto perdió

El eliminado `internal/registrar` declaraba una interfaz con la porción de la
conexión que necesitaba:

```go
type OLT interface {
    Connect() error
    EnableMode() error
    ExecuteCommand(cmd string) (string, error)
    IndicesServicePortOcupados() (map[int]bool, error)
    SaveConfig() error
    Disconnect() error
}
```

El orquestador **no dependía de `*olt.Conn`** sino de esa interfaz, y el test la
implementaba con un `mockOLT` que grababa el orden de los comandos recibidos y
permitía inyectar respuestas y errores. Así se testeaba toda la lógica de registro
**sin una OLT y sin red**.

Dos detalles idiomáticos que vale la pena retener:

- **La interfaz se declara del lado del consumidor**, no junto a la
  implementación. En Go se definen donde se usan, chicas y específicas. Proverbio:
  *"the bigger the interface, the weaker the abstraction"*.
- **Es implícita**: `*olt.Conn` la satisfacía sin declarar nada. No existe
  `implements`. Eso permite escribir interfaces para código que no controlás.

> ⚠️ **Estado actual**: `cmd/provisionar` llama a `*olt.Conn` **directamente**, sin
> interfaz de por medio, así que `ejecutarServicePorts()` y `ejecutarRegistroOnu()`
> **no tienen tests**. Recuperar el patrón (una interfaz local en `main.go` y un
> mock) es la forma más barata de volver a cubrir esa lógica.

### 11.2 `context.Context` — el que falta

Todo el proyecto usa `context.Background()`: el contexto vacío, que **no se cancela
nunca y no tiene deadline**. Una consulta que se cuelga, cuelga el programa entero,
sin timeout.

`Context` es el mecanismo estándar de Go para propagar **cancelación y plazos** a
través de las capas:

```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()                    // SIEMPRE, libera el timer asociado

macs, err := db.GetMacsHistoricasRepaso(ctx, idOlt, cfg.Query.FechaDesde)
if errors.Is(err, context.DeadlineExceeded) {
    // la consulta tardó más de 30s y se abortó
}
```

Lo bueno es que **las firmas ya lo aceptan** (`QueryContext`, `ExecContext`,
`BeginTx` reciben `ctx`) — sólo falta pasar uno de verdad en vez de
`context.Background()`.

Reglas: `ctx` es siempre el **primer** parámetro, se llama `ctx`, y **nunca** se
guarda en un struct. Se pasa de función en función.

### 11.3 Envolver errores con `%w`

El proyecto lo hace bien:

```go
return fmt.Errorf("guardando registro_onu %d/%d: %w", r.Puerto, r.OntID, err)
```

`%w` (y no `%v`) **conserva el error original** en la cadena, para que arriba de
todo `errors.Is(err, sql.ErrNoRows)` o `errors.As(err, &myErr)` sigan funcionando.
Con `%v` el error se aplana a texto y perdés la capacidad de inspeccionarlo
programáticamente.

Regla práctica: `%w` cuando el llamador podría querer distinguir el tipo de error;
`%v` cuando el error sólo se va a imprimir.

### 11.4 `defer`: dos detalles que confunden

```go
defer tx.Rollback()
defer stmtOnu.Close()
```

- **Los argumentos se evalúan en el momento del `defer`**, no al ejecutarse. Si
  hacés `defer fmt.Println(i)` dentro de un bucle, imprime el `i` de ese momento.
- **Se ejecutan en orden LIFO** (el último diferido corre primero), al retornar la
  función — no al terminar el bloque. Un `defer` dentro de un `for` **no** corre en
  cada vuelta: se acumulan todos hasta el final de la función. Si abrís recursos en
  un bucle largo, envolvé el cuerpo en una función.

---

## 12. Ejercicios

Sobre este repositorio, de menor a mayor dificultad:

1. Corré `go test -race ./...` y `go vet ./...`. ¿Reportan algo?
2. Cambiá `readUntil` a `(string, error)` y propagá el error en `ExecuteCommand`.
   ¿Cuántos llamadores dejan de compilar? Esa cantidad es cuántos lugares estaban
   asumiendo éxito en silencio.
3. Escribí un test de `internal/olt` que simule una OLT que nunca responde y
   verificá que `ExecuteCommand` devuelve error. Hoy devuelve `nil`.
4. Agregá `go.uber.org/goleak` al `TestMain` de `internal/olt` y forzá un reintento
   de conexión con muchos datos pendientes. ¿Aparece la fuga de la sección 6?
5. Hacé que `re.Match` de `readUntil` busque el prompt sólo en los últimos N bytes
   del buffer en vez de reescanearlo entero. Medí la diferencia con un benchmark
   (`go test -bench`) simulando una respuesta de 1000 líneas.
6. Reemplazá `context.Background()` por un contexto con timeout en `cmd/repaso` y
   comprobá que una consulta lenta ahora falla en vez de colgar el programa.
7. Diseñá un esquema de idempotencia para `service-port` que sobreviva a una
   entrega duplicada. Pista: el índice no puede salir de un contador en memoria.
8. Escribí la saga con compensaciones equivalente a `ReemplazarRegistros` si
   `registro_onu` y `registro_cliente` vivieran en bases distintas. Compará el
   largo con las diez líneas de la transacción actual.
