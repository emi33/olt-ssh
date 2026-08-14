// Consulta de índices 'service-port' ya usados en la OLT, para poder
// asignar índices nuevos sin colisionar con corridas anteriores de este
// programa ni con cambios hechos a mano por consola. Ver
// docs/08-comandos-olt.md para el formato real de 'show service-port'.
package olt

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// indiceServicePortRe extrae el primer entero de cada fila de datos de
// 'show service-port' (columna Index). La fila de encabezado ("Index
// PON-Port ...") no es numérica y no matchea, se descarta sola:
//
//	Index PON-Port   ONT-ID  GEM-ID  SVLAN ...
//	3698  GPON1/1/16 6       1       666   ...
var indiceServicePortRe = regexp.MustCompile(`^\s*(\d+)\s+\S+`)

// paginacionRe detecta un posible corte de paginación en la respuesta de un
// 'show' largo. El texto exacto de este firmware no está confirmado (la
// única captura disponible tenía solo 2 filas, sin paginación) — cubre los
// patrones más comunes en firmwares estilo Huawei VRP y similares. Ajustar
// si al probar contra la OLT real (1000+ filas) aparece un marcador
// distinto.
var paginacionRe = regexp.MustCompile(`(?i)--\s*more\s*--|press any key|more\s*:`)

// stopServicePortRe combina el prompt final y el marcador de paginación:
// cualquiera de los dos corta la espera de readUntil para que podamos
// decidir si hay que enviar una tecla de continuación o si ya terminamos.
var stopServicePortRe = regexp.MustCompile(paginacionRe.String() + "|" + promptRe.String())

// IndicesServicePortOcupados consulta 'show service-port' y devuelve el set
// de índices ya usados en la OLT.
func (c *Conn) IndicesServicePortOcupados() (map[int]bool, error) {
	// Intento best-effort de desactivar paginación; si el firmware no
	// soporta el comando, la OLT responde con un error que se ignora (no
	// aborta la consulta real, que sigue con el manejo defensivo de
	// paginación de executeCommandPaginado).
	_, _ = c.executeCommand("screen-length 0 temporary", 5*time.Second)

	raw, err := c.executeCommandPaginado("show service-port", 30*time.Second)
	if err != nil {
		return nil, fmt.Errorf("consultando service-port: %w", err)
	}

	return parseIndicesServicePort(raw), nil
}

// parseIndicesServicePort extrae los índices de la respuesta ya limpia de
// 'show service-port'. Separada de IndicesServicePortOcupados para poder
// testearla sin necesitar una conexión SSH real.
func parseIndicesServicePort(raw string) map[int]bool {
	ocupados := make(map[int]bool)
	for _, linea := range strings.Split(raw, "\n") {
		m := indiceServicePortRe.FindStringSubmatch(linea)
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		ocupados[n] = true
	}
	return ocupados
}

// executeCommandPaginado es como executeCommand pero maneja paginación: si
// detecta el marcador de "more" en lo acumulado, envía un espacio para que
// la OLT continúe y sigue leyendo, hasta llegar al prompt final o vencer el
// timeout total.
func (c *Conn) executeCommandPaginado(command string, timeout time.Duration) (string, error) {
	if c.stdin == nil {
		return "", fmt.Errorf("la conexión no está establecida")
	}
	if _, err := c.stdin.Write([]byte(command + "\r")); err != nil {
		return "", fmt.Errorf("escribiendo comando %q: %w", command, err)
	}
	time.Sleep(100 * time.Millisecond)

	deadline := time.Now().Add(timeout)
	var acumulado strings.Builder
	for {
		restante := time.Until(deadline)
		if restante <= 0 {
			break
		}
		chunk := c.readUntil(stopServicePortRe, restante)
		acumulado.WriteString(chunk)
		limpio := cleanANSI(acumulado.String())

		if paginacionRe.MatchString(limpio) {
			if _, err := c.stdin.Write([]byte(" ")); err != nil {
				return limpio, fmt.Errorf("enviando continuación de paginación: %w", err)
			}
			// Conserva todo lo leído hasta ahora, solo descarta el
			// marcador para no re-matchearlo en la siguiente vuelta.
			acumulado.Reset()
			acumulado.WriteString(paginacionRe.ReplaceAllString(limpio, ""))
			time.Sleep(100 * time.Millisecond)
			continue
		}

		if promptRe.MatchString(limpio) {
			c.log("Comando: " + command)
			if c.logger != nil {
				c.logger.Command(command, limpio)
			}
			return limpio, nil
		}
	}

	return cleanANSI(acumulado.String()), fmt.Errorf("timeout esperando fin de '%s'", command)
}
