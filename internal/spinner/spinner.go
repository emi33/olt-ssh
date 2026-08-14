// Package spinner muestra un indicador de carga giratorio mientras corre una
// operación lenta (consultas a la BD, guardado en lote). Uso:
//
//	sp := spinner.Start("Consultando la base de datos")
//	// ... trabajo ...
//	sp.Stop()
package spinner

import (
	"fmt"
	"sync"
	"time"
)

// Spinner es un indicador de carga activo.
type Spinner struct {
	mensaje string
	stop    chan struct{}
	wg      sync.WaitGroup
}

// Start arranca el spinner con un mensaje y devuelve el handle para pararlo.
func Start(mensaje string) *Spinner {
	s := &Spinner{mensaje: mensaje, stop: make(chan struct{})}
	s.wg.Add(1)
	go s.run()
	return s
}

func (s *Spinner) run() {
	defer s.wg.Done()
	frames := []rune{'⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏'}
	t := time.NewTicker(90 * time.Millisecond)
	defer t.Stop()
	i := 0
	for {
		select {
		case <-s.stop:
			// Limpia la línea y deja el mensaje con un check.
			fmt.Printf("\r\033[K%s ✓\n", s.mensaje)
			return
		case <-t.C:
			fmt.Printf("\r\033[K%s %c", s.mensaje, frames[i%len(frames)])
			i++
		}
	}
}

// Stop detiene el spinner y espera a que termine de limpiar la línea.
func (s *Spinner) Stop() {
	close(s.stop)
	s.wg.Wait()
}
