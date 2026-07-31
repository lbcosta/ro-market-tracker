package gnjoy

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestRetryAfterDuration(t *testing.T) {
	tests := []struct {
		name       string
		retryAfter string
		attempt    int
		want       time.Duration
	}{
		{name: "segundos", retryAfter: "5", want: 5 * time.Second},
		{name: "zero segundos", retryAfter: "0", want: 0},
		{
			name:       "segundos acima do teto são limitados",
			retryAfter: "100000",
			want:       maxRetryAfter,
		},
		{
			// Um Retry-After negativo é inválido; o client cai no backoff.
			name:       "segundos negativos caem no backoff",
			retryAfter: "-3",
			attempt:    0,
			want:       time.Second,
		},
		{
			name:       "cabeçalho ausente usa backoff exponencial",
			retryAfter: "",
			attempt:    0,
			want:       time.Second,
		},
		{name: "backoff dobra a cada tentativa", retryAfter: "", attempt: 1, want: 2 * time.Second},
		{name: "backoff na terceira tentativa", retryAfter: "", attempt: 2, want: 4 * time.Second},
		{
			name:       "backoff também respeita o teto",
			retryAfter: "",
			attempt:    20,
			want:       maxRetryAfter,
		},
		{
			name:       "valor inválido cai no backoff",
			retryAfter: "logo mais",
			attempt:    1,
			want:       2 * time.Second,
		},
		{
			// Uma data já passada não pede espera nenhuma; o client cai no
			// backoff em vez de tentar imediatamente em loop.
			name:       "data HTTP no passado cai no backoff",
			retryAfter: time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat),
			attempt:    0,
			want:       time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := retryAfterDuration(tt.retryAfter, tt.attempt); got != tt.want {
				t.Errorf("retryAfterDuration(%q, %d) = %v, quero %v", tt.retryAfter, tt.attempt, got, tt.want)
			}
		})
	}
}

// TestRetryAfterDurationDataFutura fica fora da tabela porque o valor exato
// depende do relógio: o que dá para afirmar é a faixa.
func TestRetryAfterDurationDataFutura(t *testing.T) {
	retryAfter := time.Now().Add(30 * time.Second).UTC().Format(http.TimeFormat)

	got := retryAfterDuration(retryAfter, 0)
	if got <= 25*time.Second || got > 31*time.Second {
		t.Errorf("retryAfterDuration = %v, quero algo em torno de 30s", got)
	}
}

func TestCapDuration(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want time.Duration
	}{
		{in: 5 * time.Second, want: 5 * time.Second},
		{in: maxRetryAfter, want: maxRetryAfter},
		{in: 10 * time.Hour, want: maxRetryAfter},
		{in: -time.Second, want: 0},
	}
	for _, tt := range tests {
		if got := capDuration(tt.in); got != tt.want {
			t.Errorf("capDuration(%v) = %v, quero %v", tt.in, got, tt.want)
		}
	}
}

func TestTruncateBody(t *testing.T) {
	curto := []byte("tudo certo")
	if got := truncateBody(curto); got != "tudo certo" {
		t.Errorf("truncateBody = %q, quero o corpo intacto", got)
	}

	longo := make([]byte, 5000)
	for i := range longo {
		longo[i] = 'x'
	}
	if got := truncateBody(longo); len(got) != 500 {
		t.Errorf("len(truncateBody) = %d, quero 500", len(got))
	}
}

// TestIsStaleActionIDErr documenta quais falhas o client interpreta como "o
// site foi redeployado e o hash da action mudou" — o gatilho da redescoberta
// automática.
func TestIsStaleActionIDErr(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"404 do Next.js", &HTTPError{StatusCode: http.StatusNotFound}, true},
		{"500 do upstream", &HTTPError{StatusCode: http.StatusInternalServerError}, true},
		{"502 do upstream", &HTTPError{StatusCode: http.StatusBadGateway}, true},
		{"resposta 200 fora do formato esperado", ErrFieldsNotFound, true},
		{"erro embrulhado", errors.Join(errors.New("contexto"), ErrFieldsNotFound), true},
		// 429 é rate limiting, não hash desatualizado: redescobrir só geraria
		// mais requisições contra um site que já está pedindo calma.
		{"429 não é hash desatualizado", &HTTPError{StatusCode: http.StatusTooManyRequests}, false},
		{"400 não é hash desatualizado", &HTTPError{StatusCode: http.StatusBadRequest}, false},
		{"erro de rede qualquer", errors.New("connection refused"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isStaleActionIDErr(tt.err); got != tt.want {
				t.Errorf("isStaleActionIDErr(%v) = %v, quero %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestDedupeStrings(t *testing.T) {
	in := []string{"a.js", "b.js", "a.js", "c.js", "b.js"}
	got := dedupeStrings(in)

	want := []string{"a.js", "b.js", "c.js"}
	if len(got) != len(want) {
		t.Fatalf("dedupeStrings = %v, quero %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("dedupeStrings[%d] = %q, quero %q (a ordem original importa)", i, got[i], want[i])
		}
	}
}
