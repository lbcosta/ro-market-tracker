package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/lbcosta/ro-market-tracker/internal/gnjoytest"
)

// TestSearchOfereceAsOutrasVersoes cobre o caso em que a busca acha alguma
// coisa e, mesmo assim, esconde o que o usuário procurava: das sete versões da
// "Caixa de Armadura" que existem no servidor, só a "+7" está anunciada agora.
// Quem quer a "+13" precisa de um caminho para colocá-la na watchlist — e a
// tela de histórico, que é onde as versões fora do mercado aparecem, só é
// mostrada quando a busca não acha NADA.
func TestSearchOfereceAsOutrasVersoes(t *testing.T) {
	srv, mock := newWebServer(t)

	q := url.Values{"server": {"NIDHOGG"}, "item": {"Caixa de Armadura"}}
	_, html := getHTML(t, srv, "/web/search?"+q.Encode())

	wantContains(t, html,
		"Ver outras versões deste item fora do mercado",
		// O link já leva os itemIds que a tabela mostra, para o fragmento não
		// repetir o que está logo acima na tela — e para descobrir isso não
		// custar outra busca de mercado.
		`hx-get="/web/search/variants?item=Caixa&#43;de&#43;Armadura&amp;server=NIDHOGG&amp;shown=22926"`,
	)

	// O bloco é um link, não conteúdo: a busca continua custando uma
	// requisição, e as outras versões só são consultadas se alguém clicar.
	if got := mock.RequestCount(); got != 1 {
		t.Errorf("a busca custou %d requisições, quero 1", got)
	}
}

func TestVariants(t *testing.T) {
	srv, _ := newWebServer(t)

	q := url.Values{"server": {"NIDHOGG"}, "item": {"Caixa de Armadura"}, "shown": {"22926"}}
	resp, html := getHTML(t, srv, "/web/search/variants?"+q.Encode())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, quero 200", resp.StatusCode)
	}

	wantContains(t, html,
		"Caixa de Armadura &#43;13",
		"Caixa de Armadura &#43;5",
		"50.000.000 z",
		// Cada versão pode ir para a watchlist esperando aparecer no mercado —
		// o mesmo modo da tabela de histórico.
		`data-mode="availability"`,
		`data-item-id="22932"`,
	)

	// A versão que já está na tabela de resultados não se repete aqui.
	if strings.Contains(html, "Caixa de Armadura &#43;7") {
		t.Errorf("a versão já mostrada na busca não deveria aparecer de novo:\n%s", html)
	}
}

// TestVariantsSemOutrasVersoes: quando tudo que existe já está na tela, o
// fragmento diz isso em vez de abrir uma tabela vazia.
func TestVariantsSemOutrasVersoes(t *testing.T) {
	srv, _ := newWebServer(t)

	q := url.Values{
		"server": {"NIDHOGG"}, "item": {"Caixa de Armadura"},
		"shown": {"22926,22932,22924"},
	}
	_, html := getHTML(t, srv, "/web/search/variants?"+q.Encode())

	wantContains(t, html, "Nenhuma outra versão deste item foi vendida no servidor NIDHOGG.")
	if strings.Contains(html, "results-table") {
		t.Errorf("sem versões a mostrar, não deveria haver tabela:\n%s", html)
	}
}

func TestVariantsParametrosFaltando(t *testing.T) {
	srv, mock := newWebServer(t)

	_, html := getHTML(t, srv, "/web/search/variants?server=NIDHOGG")
	wantContains(t, html, `class="error"`, "Informe o servidor e o nome do item.")
	if got := mock.RequestCount(); got != 0 {
		t.Errorf("houve %d requisições ao upstream, quero 0", got)
	}
}

func TestVariantsErroDoUpstream(t *testing.T) {
	srv, mock := newWebServer(t)
	mock.QueueFailure(gnjoytest.Failure{Status: http.StatusInternalServerError}, 10)

	q := url.Values{"server": {"NIDHOGG"}, "item": {"Caixa de Armadura"}}
	resp, html := getHTML(t, srv, "/web/search/variants?"+q.Encode())

	// O fragmento é trocado no DOM pelo HTMX, então precisa vir com 200 e a
	// mensagem de erro dentro.
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, quero 200 (o HTMX precisa trocar o fragmento)", resp.StatusCode)
	}
	wantContains(t, html, `class="error"`, "Não foi possível consultar as outras versões agora.")
}
