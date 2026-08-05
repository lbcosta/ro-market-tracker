package web

import (
	"context"
	"log/slog"
	"time"

	"github.com/lbcosta/ro-market-tracker/internal/gnjoy"
)

// defaultProbeInterval é de quanto em quanto tempo, enquanto as consultas
// estiverem suspensas, uma requisição mínima é gasta para descobrir se o site
// voltou a responder.
//
// Dez minutos é longo de propósito: o 429 que chega a suspender é o site
// dizendo que a cota acabou, e sondar de minuto em minuto contra um bloqueio
// desses é o mesmo comportamento que causou o bloqueio.
const defaultProbeInterval = 10 * time.Minute

// StartSuspensionProbe cuida da volta ao normal: enquanto as consultas
// estiverem suspensas por um 429 (ver gnjoy/suspend.go), sonda o site de tempos
// em tempos até ele voltar a responder, e então libera tudo sozinho.
//
// A goroutine é reativa, não um ticker permanente: no caso normal — que é o
// caso quase sempre — ela fica parada esperando uma suspensão que não vem, sem
// acordar nem gastar requisição. E como só o binário a inicia, nenhum teste
// que não a chame explicitamente ganha atividade de fundo.
//
// Encerra quando ctx é cancelado. Hoje o binário passa um context.Background():
// não há shutdown ordenado para pendurar isto (o servidor sobe com http.Serve
// direto), mas o parâmetro deixa a costura pronta para quando houver.
func StartSuspensionProbe(ctx context.Context, client *gnjoy.Client, interval time.Duration) {
	if interval <= 0 {
		interval = defaultProbeInterval
	}

	state := client.Suspension()
	updates, unsubscribe := state.Subscribe()

	go func() {
		defer unsubscribe()
		for {
			select {
			case <-ctx.Done():
				return
			case s, ok := <-updates:
				if !ok {
					return
				}
				if s.Suspended {
					sondarAteLiberar(ctx, client, interval)
				}
			}
		}
	}()
}

// sondarAteLiberar gasta uma requisição a cada interval até o site voltar. Não
// é ela que reabre as consultas: quem reabre é o próprio client, ao receber uma
// resposta que não seja 429 (ver gnjoy/suspend.go). Aqui só se observa o
// resultado.
func sondarAteLiberar(ctx context.Context, client *gnjoy.Client, interval time.Duration) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}

		if !client.Suspension().Current().Suspended {
			return
		}

		// Timeout do tamanho do intervalo: a sonda atravessa a suspensão, mas
		// não a calmaria — se o site mandou um "Retry-After" longo, ela ficaria
		// estacionada esperando a vez e viraria uma goroutine zumbi, com a
		// próxima rodada empilhando em cima.
		probeCtx, cancel := context.WithTimeout(ctx, interval)
		err := client.ProbeUpstream(probeCtx)
		cancel()

		if err != nil {
			slog.Debug("web: o site ainda está limitando as consultas", "error", err)
			continue
		}
		slog.Info("web: o site voltou a aceitar consultas, retomando")
		return
	}
}
