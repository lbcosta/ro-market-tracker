// Motor do rodízio de consultas — o relógio compartilhado por TODAS as telas
// que vigiam preço.
//
// POR QUE ELE É UM SÓ
//
// O site da GnJoy limita quantas consultas aceita, e o programa inteiro se
// segura em UMA requisição por minuto para não tomar 429. Esse teto é do
// processo, não de uma tela: se a watchlist e o estoque tivessem cada um o
// próprio cronômetro, o tráfego dobraria sem ninguém ter decidido isso. Por
// isso o rodízio mora aqui, e as telas se registram como "fontes".
//
// A cada tick, o motor escolhe UMA entrada — a que está há mais tempo sem
// consulta, entre todas as fontes — e consulta só ela. O intervalo entre duas
// consultas do MESMO item cresce com o total vigiado (~N × 1 min), e é por
// isso que existe um teto conjunto (MONITOR_MAX_ITENS).
//
// UMA REGRA QUE NÃO PODE SER QUEBRADA
//
// Uma fonte nunca pode desistir de uma consulta porque a linha não está na
// tela. Qual aba está aberta é escolha de quem olha; o que é vigiado é
// escolha de quem configurou. Foi exatamente esse acoplamento que fazia o
// rodízio virar um nada com a aba Estoque aberta — o tick escolhia sempre a
// mesma entrada, não consultava, não avançava o relógio dela, e o aviso no
// Telegram nunca saía. Ver o comentário em fetchLivePrice (watchlist.js).

const MONITOR_TICK_MS = 60 * 1000;

// Teto CONJUNTO de itens vigiados, somando todas as fontes. É conjunto porque
// o que ele protege é o intervalo de revezamento, e esse intervalo não sabe de
// que tela o item veio: 50 itens vigiados já significam quase uma hora entre
// uma consulta e a seguinte do mesmo item. Itens desligados não contam — eles
// não disputam a vez.
const MONITOR_MAX_ITENS = 50;

// Uma fonte é uma tela que tem itens a consultar:
//   nome        — só para mensagem de erro e depuração
//   listar()    — as entradas ELEGÍVEIS agora (já filtradas por ligado/desligado),
//                 cada uma com id e lastCheckedAt
//   consultar(entrada, fresh) — Promise; precisa gravar lastCheckedAt mesmo
//                 quando a consulta falha, senão a entrada trava a fila
const fontes = [];

function registrarFonte(fonte) {
  fontes.push(fonte);
}

function entradasElegiveis() {
  const todas = [];
  for (const fonte of fontes) {
    for (const entrada of fonte.listar()) {
      todas.push({ fonte, entrada });
    }
  }
  return todas;
}

// totalMonitorados é o que o teto conjunto compara. Conta as elegíveis, ou
// seja, as que de fato disputam a vez — cadastrar itens desligados é livre.
function totalMonitorados() {
  return entradasElegiveis().length;
}

// podeMonitorarMais é o que cada tela pergunta antes de LIGAR um item. A
// resposta é do processo inteiro, não da tela que perguntou.
function podeMonitorarMais() {
  return totalMonitorados() < MONITOR_MAX_ITENS;
}

// escolherProxima devolve a entrada há mais tempo sem consulta, entre todas as
// fontes — nunca consultada conta como a mais antiga de todas. Empate é
// desfeito pela ordem de registro das fontes e, dentro de uma, pela ordem da
// lista (que já é a ordem de exibição).
//
// Isto substitui um índice/cursor de rodízio explícito: o revezamento se
// ajusta sozinho a remoções e adições, porque cada entrada carrega consigo
// mesma "quando foi a última vez" — não há estado externo para reconciliar.
function escolherProxima() {
  let escolhida = null;
  for (const candidata of entradasElegiveis()) {
    const quando = candidata.entrada.lastCheckedAt || 0;
    if (escolhida == null || quando < (escolhida.entrada.lastCheckedAt || 0)) {
      escolhida = candidata;
    }
  }
  return escolhida;
}

// rodizioSuspenso espelha o estado que o servidor publica pelo stream de
// atividade (ver activity-bar.js): enquanto o site estiver limitando as
// consultas, o ciclo automático para de sair.
let rodizioSuspenso = false;

// monitorRodando é a trava de reentrada: um tick lento não pode ser
// atropelado pelo seguinte, senão duas consultas sairiam juntas e o teto de
// uma por minuto deixaria de valer.
let monitorRodando = false;

let proximaRodadaEm = null;
let rodizioTimerId = null;

// rodarTick consulta UMA entrada — a escolhida por escolherProxima —, nunca a
// lista inteira de uma vez. É o que garante o ritmo constante de uma consulta
// por minuto, não importa quantos itens estejam vigiados.
async function rodarTick(fresh = false) {
  if (monitorRodando || rodizioSuspenso) return;
  monitorRodando = true;
  try {
    const escolha = escolherProxima();
    if (escolha) await escolha.fonte.consultar(escolha.entrada, fresh);
  } finally {
    monitorRodando = false;
  }
}

// agendarRodizio (re)agenda o próximo tick usando setTimeout (em vez de
// setInterval) para que retomar de uma suspensão possa cancelar a espera
// pendente e recomeçar a contagem do zero, sem deixar um tick duplicado
// rodando em paralelo. O tick seguinte só é agendado quando o atual termina,
// então uma consulta lenta (site devagar) atrasa o próximo em vez de se
// sobrepor a ele.
function agendarRodizio(delayMs = MONITOR_TICK_MS) {
  if (rodizioTimerId) clearTimeout(rodizioTimerId);
  proximaRodadaEm = Date.now() + delayMs;
  rodizioTimerId = setTimeout(async () => {
    await rodarTick();
    agendarRodizio();
  }, delayMs);
  pintarCronometros();
}

function suspenderRodizio(suspenso) {
  const era = rodizioSuspenso;
  rodizioSuspenso = suspenso;

  if (suspenso) {
    // Sem cancelar o setTimeout pendente, ele dispara mesmo assim: encontra
    // rodarTick recusando (correto, sem custo — ver acima), mas reagenda
    // outro tick de qualquer forma, e o cronômetro volta a contar como se a
    // checagem automática continuasse rodando normalmente. É exatamente essa
    // contagem fantasma que confundia quem olhava a tela durante um bloqueio.
    if (rodizioTimerId) clearTimeout(rodizioTimerId);
    rodizioTimerId = null;
    proximaRodadaEm = null;
    pintarCronometros();
    pintarTitulosDoCronometro("Pausado: o site está limitando as consultas");
    return;
  }

  pintarTitulosDoCronometro("Tempo até a próxima checagem automática de preços");
  // Ao voltar ao normal, uma checagem imediata: a última pode ter sido
  // interrompida no meio, e esperar mais um minuto por um dado que já dá para
  // buscar seria gratuito. Também é o que tira o cronômetro do "--:--".
  if (era) {
    rodarTick(true);
    agendarRodizio();
  }
}

// Os cronômetros são achados por atributo, e não por id: o relógio é do
// rodízio inteiro, não da watchlist, e qualquer tela pode mostrá-lo sem que
// este arquivo saiba o id do elemento dela.
function pintarCronometros() {
  const alvos = document.querySelectorAll("[data-cronometro-do-rodizio]");
  if (alvos.length === 0) return;

  let texto;
  if (proximaRodadaEm == null) {
    // Sem ciclo agendado — nem antes do primeiro, nem durante uma suspensão.
    // Mesmo texto nos dois casos: não há nada de fato contando.
    texto = "--:--";
  } else {
    const restanteMs = Math.max(0, proximaRodadaEm - Date.now());
    const totalSegundos = Math.ceil(restanteMs / 1000);
    const minutos = Math.floor(totalSegundos / 60);
    const segundos = totalSegundos % 60;
    texto = String(minutos).padStart(2, "0") + ":" + String(segundos).padStart(2, "0");
  }

  for (const alvo of alvos) alvo.textContent = texto;
}

function pintarTitulosDoCronometro(titulo) {
  for (const alvo of document.querySelectorAll("[data-cronometro-do-rodizio-titulo]")) {
    alvo.title = titulo;
  }
}

document.addEventListener("DOMContentLoaded", () => {
  // As fontes se registram no topo dos próprios arquivos, que rodam antes
  // deste evento (todos os scripts são "defer", então executam na ordem do
  // documento — ver layout.html.tmpl). Aqui a lista já está completa.
  //
  // A primeira consulta sai na hora, sem esperar o primeiro minuto: quem
  // abriu a página já viu o último resultado conhecido de cada item, pintado
  // do localStorage, e o único dado que falta é o da entrada mais atrasada.
  rodarTick();
  agendarRodizio();
  setInterval(pintarCronometros, 1000);
});
