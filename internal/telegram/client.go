// Package telegram envia mensagens pela Bot API do Telegram — hoje só a
// notificação de acerto da watchlist (ver internal/web/telegram.go). Não é
// um client genérico da Bot API, só o suficiente para sendMessage.
package telegram

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultBaseURL é a API oficial do Telegram. Uma constante separada existe
// só para os testes poderem apontar para um httptest.Server local (ver
// WithBaseURL), sem bater na API de verdade.
const DefaultBaseURL = "https://api.telegram.org"

// Client manda mensagens para um bot/chat fixos, definidos na criação.
type Client struct {
	baseURL    string
	botToken   string
	chatID     string
	httpClient *http.Client
}

// Option configura um Client — hoje só usada pelos testes.
type Option func(*Client)

// WithBaseURL troca a API oficial por outra (um httptest.Server local, nos
// testes).
func WithBaseURL(baseURL string) Option {
	return func(c *Client) { c.baseURL = baseURL }
}

// New cria um Client para o bot/chat informados. botToken e chatID vazios
// não são validados aqui — quem decide se a integração está configurada é
// Config.Configured (ver config.go); um Client "vazio" não deveria ser
// construído fora dos testes.
func New(botToken, chatID string, opts ...Option) *Client {
	c := &Client{
		baseURL:    DefaultBaseURL,
		botToken:   botToken,
		chatID:     chatID,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// HTTPError é devolvido quando a API do Telegram responde algo diferente de
// 200 — o corpo costuma trazer a "description" do erro (token inválido,
// chat_id errado, bot bloqueado pelo usuário etc.), útil no log de quem
// chamou.
type HTTPError struct {
	StatusCode int
	Body       string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("telegram: status %d: %s", e.StatusCode, e.Body)
}

// Send manda text como mensagem para o chat configurado, com parse_mode=HTML:
// quem monta o texto (ver buildTelegramText em watchlist.js) já entrega as
// tags prontas (<b>, <code>) e escapa o que for dinâmico (nome do item, da
// loja) — o client não entende de watchlist, só repassa o texto como veio.
// HTML, e não o Markdown do Telegram, porque nome de item pode ter
// caracteres que quebram a sintaxe do Markdown ("+", "[", "*"), e HTML só
// tem três (&, <, >) para escapar.
func (c *Client) Send(ctx context.Context, text string) error {
	endpoint := c.baseURL + "/bot" + c.botToken + "/sendMessage"

	form := url.Values{}
	form.Set("chat_id", c.chatID)
	form.Set("text", text)
	form.Set("parse_mode", "HTML")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("telegram: montando requisição: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("telegram: executando requisição: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return &HTTPError{StatusCode: resp.StatusCode, Body: string(body)}
	}
	return nil
}
