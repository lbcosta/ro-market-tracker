// Package web serve o frontend HTMX do ro-market-tracker: uma página de
// busca que consulta o gnjoy.Client diretamente e renderiza fragmentos HTML
// (não JSON) para o HTMX trocar no DOM — a API JSON em internal/api continua
// existindo separadamente para uso programático.
package web

import (
	"embed"
	"html/template"
	"io/fs"
	"math"
	"net/http"
	"strconv"
)

//go:embed templates/*.tmpl
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

var templates = template.Must(template.New("").Funcs(template.FuncMap{
	"formatMoney":  formatMoney,
	"formatMoneyF": formatMoneyF,
}).ParseFS(templateFS, "templates/*.tmpl"))

// formatMoney formata um preço em zeny com "." como separador de milhar,
// no padrão usado pela comunidade de Ragnarok Online (ex.: 1.500.000 z).
func formatMoney(n int64) string {
	return groupThousands(n) + " z"
}

// formatMoneyF é formatMoney para valores agregados (médias, desvio
// padrão), arredondados para o zeny mais próximo antes de formatar — a
// moeda do jogo não tem frações.
func formatMoneyF(f float64) string {
	return formatMoney(int64(math.Round(f)))
}

func groupThousands(n int64) string {
	neg := n < 0
	if neg {
		n = -n
	}
	s := strconv.FormatInt(n, 10)

	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, '.')
		}
		out = append(out, c)
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}

// staticHandler serve os assets estáticos embutidos (templates/htmx, CSS,
// JS) a partir da raiz de static/, sem o prefixo "static/" do embed.FS.
func staticHandler() http.Handler {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	return http.FileServerFS(sub)
}
