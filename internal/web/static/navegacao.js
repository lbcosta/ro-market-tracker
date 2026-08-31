// Troca de abas (Estoque | Watchlist) sem recarregar o documento.
//
// POR QUE NÃO É NAVEGAÇÃO NORMAL DO NAVEGADOR
//
// Quem fala com o site da GnJoy é um motor só, no servidor (o gnjoy.Client),
// compartilhado por tudo. A barra de atividades no rodapé é a janela para
// ele, alimentada por um EventSource, e o rodízio de preços da watchlist é
// um relógio que precisa correr continuamente. Recarregar o documento a cada
// clique no menu quebrava os três de uma vez:
//
//   - abria uma conexão SSE nova por navegação (medido: duas por ida e volta
//     entre as abas), deixando as anteriores penduradas até o navegador
//     recolhê-las — e o HTTP/1.1 só permite seis conexões por host, então
//     depois de algumas trocas a próxima página ficava esperando um socket
//     vagar, sem nunca terminar de carregar;
//   - zerava o log na tela, que é justamente o histórico do que o motor
//     andou fazendo;
//   - reiniciava o watchlist.js, e com ele o rodízio: cada volta à aba
//     Watchlist gastava uma consulta ao site e recomeçava o cronômetro de um
//     minuto do zero. Trocar de aba rápido consultava muito mais que uma vez
//     por minuto, que é o caminho curto para o 429.
//
// Então aqui a navegação troca só o miolo (#page-content). Cabeçalho, menu,
// barra de atividades e versão ficam onde estão, no mesmo DOM, do começo ao
// fim da sessão.
//
// POR QUE NÃO hx-get NOS LINKS
//
// O histórico do htmx trabalha no <body> inteiro: no "voltar" ele restaura o
// body a partir de um snapshot, o que levaria junto a barra de atividades —
// exatamente o que não pode ser re-renderizada. Por isso o push/pop de
// histórico é feito aqui, e o htmx entra só para buscar e processar o
// fragmento (htmx.ajax processa os hx-* que vêm dentro dele, como o hx-get
// do formulário de busca).

const CONTEUDO = "#page-content";

// paginaAtual identifica a aba aberta, para um clique na aba já aberta não
// virar uma requisição e um swap inúteis.
function paginaAtual() {
  return window.location.pathname;
}

// irPara busca o corpo da página e o troca no lugar do atual. O menu volta
// junto, fora de banda (hx-swap-oob no <nav> — ver templates/layout.html.tmpl),
// então a aba ativa continua sendo decidida no servidor.
async function irPara(url) {
  await htmx.ajax("GET", url, { target: CONTEUDO, swap: "innerHTML" });
}

// Depois de cada troca, o corpo novo precisa ser religado: o painel da
// watchlist é DOM recém-criado (sem linhas e sem ouvintes) e os controles que
// uma suspensão desabilita voltaram habilitados.
//
// As duas funções vêm de watchlist.js e activity-bar.js; o typeof protege o
// caso de um script não ter carregado, em vez de deixar a navegação inteira
// morrer num ReferenceError.
function religarCorpo() {
  if (typeof montarPainelDaWatchlist === "function") montarPainelDaWatchlist();
  if (typeof montarPainelDoEstoque === "function") montarPainelDoEstoque();
  if (typeof reapplySuspension === "function") reapplySuspension();
}

document.addEventListener("DOMContentLoaded", () => {
  // Ouvinte no document, e não no <nav> nem em cada <a>: o menu inteiro é
  // substituído fora de banda a cada troca, então qualquer ouvinte preso
  // dentro dele morre junto na primeira navegação — e a partir da segunda o
  // clique voltaria a ser navegação comum do navegador, recarregando o
  // documento (foi exatamente o que aconteceu quando este ouvinte ficava no
  // <nav>: a primeira troca funcionava, a segunda recarregava a página e
  // abria uma conexão SSE nova).
  document.addEventListener("click", (ev) => {
    const aba = ev.target.closest?.(".page-tab");
    if (!aba) return;

    // Cliques que o usuário espera que o NAVEGADOR trate: abrir em outra aba
    // (Ctrl/Cmd), em outra janela (Shift) ou pelo botão do meio. Interceptar
    // esses quebraria o link.
    if (ev.metaKey || ev.ctrlKey || ev.shiftKey || ev.altKey || ev.button !== 0) return;

    ev.preventDefault();
    const destino = aba.getAttribute("href");
    if (destino === paginaAtual()) return;

    // pushState antes do swap, e não depois: se a requisição falhar, a URL
    // volta pelo popstate junto com o conteúdo; se fosse depois, uma falha
    // deixaria a barra de endereços mentindo sobre a aba aberta até o
    // próximo clique.
    history.pushState({ pagina: destino }, "", destino);
    irPara(destino);
  });

  // "Voltar" e "avançar": mesma troca, sem empilhar histórico de novo.
  window.addEventListener("popstate", () => irPara(paginaAtual()));

  // htmx dispara isto para QUALQUER swap da página (uma busca, um card
  // expandindo), então religar só interessa quando o alvo foi o miolo.
  document.body.addEventListener("htmx:afterSwap", (ev) => {
    if (ev.target && ev.target.id === "page-content") religarCorpo();
  });
});
