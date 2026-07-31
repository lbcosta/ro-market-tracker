// Comando mockgnjoy sobe o site falso do GnJoy LATAM
// (internal/gnjoytest) como um processo de verdade, para que os testes de
// navegador possam apontar o servidor real (cmd/server, via GNJOY_BASE_URL)
// para ele em vez da API real.
//
// Além das rotas do site falso, expõe um pequeno plano de controle sob
// /__mock/ para que o teste consiga injetar falhas do upstream e inspecionar
// o que o servidor pediu — coisas que, nos testes de Go, são feitas chamando
// os métodos do Mock diretamente.
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/lbcosta/ro-market-tracker/internal/gnjoytest"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:0", "endereço de escuta (porta 0 escolhe uma livre)")
	flag.Parse()

	mock := gnjoytest.NewMock(gnjoytest.DemoConfig())

	mux := http.NewServeMux()
	mux.HandleFunc("POST /__mock/fail", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		status, err := strconv.Atoi(q.Get("status"))
		if err != nil {
			http.Error(w, "'status' deve ser um número inteiro", http.StatusBadRequest)
			return
		}
		times := 1
		if raw := q.Get("times"); raw != "" {
			if times, err = strconv.Atoi(raw); err != nil {
				http.Error(w, "'times' deve ser um número inteiro", http.StatusBadRequest)
				return
			}
		}
		mock.QueueFailure(gnjoytest.Failure{
			Status:     status,
			RetryAfter: q.Get("retryAfter"),
		}, times)
		w.WriteHeader(http.StatusNoContent)
	})
	// Atrasa as próximas respostas sem falhá-las: é o que permite observar de
	// forma determinística o estado "em andamento" na barra de atividades,
	// em vez de tentar acertar uma janela de milissegundos.
	mux.HandleFunc("POST /__mock/delay", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		ms, err := strconv.Atoi(q.Get("ms"))
		if err != nil {
			http.Error(w, "'ms' deve ser um número inteiro", http.StatusBadRequest)
			return
		}
		times := 1
		if raw := q.Get("times"); raw != "" {
			if times, err = strconv.Atoi(raw); err != nil {
				http.Error(w, "'times' deve ser um número inteiro", http.StatusBadRequest)
				return
			}
		}
		mock.QueueFailure(gnjoytest.Failure{
			Passthrough: true,
			Delay:       time.Duration(ms) * time.Millisecond,
		}, times)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /__mock/requests", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"count":%d}`, mock.RequestCount())
	})
	// Devolve o mock ao estado inicial: sem histórico de requisições e sem
	// falhas ou atrasos pendentes. Os testes de navegador chamam isso antes
	// de cada caso, para que nenhum consiga estragar o seguinte.
	mux.HandleFunc("POST /__mock/reset", func(w http.ResponseWriter, r *http.Request) {
		mock.ResetRequests()
		mock.ClearFailures()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.Handle("/", mock)

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("mockgnjoy: não foi possível escutar em %s: %v", *addr, err)
	}
	// Endereço e action id vão para o stdout em linhas previsíveis: é assim
	// que o setup dos testes de navegador descobre onde o mock subiu e com
	// qual hash de Server Action configurar o servidor — sem precisar
	// duplicar a constante do lado do JavaScript.
	fmt.Printf("mockgnjoy listening on http://%s\n", ln.Addr().String())
	fmt.Printf("mockgnjoy action id %s\n", mock.ActionID())

	if err := http.Serve(ln, mux); err != nil {
		log.Fatalf("mockgnjoy: servidor encerrado: %v", err)
	}
}
