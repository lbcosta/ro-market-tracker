// Precificação: o que responder a quem está decidindo por quanto anunciar.
//
// A decisão de quem vende não é "qual é o preço justo" — é um trade-off:
// preço alto pode não vender, preço baixo vende rápido e deixa dinheiro na
// mesa. Um número sugerido sozinho esconde exatamente a escolha que o usuário
// precisa fazer. Por isso aqui saem uma FAIXA e CENÁRIOS, cada um com o tempo
// que se espera até vender e a aposta que ele embute.
//
// O DEFEITO DO DADO DE ORIGEM, QUE MANDA EM TUDO AQUI
//
// O histórico de vendas é indexado por itemId, mas refino e encantamento são
// propriedades da UNIDADE. Uma bota +0 e uma bota +9 com encantamento raro
// são o mesmo itemId, então o histórico de um equipamento não é uma
// distribuição — são várias empilhadas, e a média cai no vazio entre elas.
// O site não expõe o que a unidade tinha quando foi vendida, então isso não
// tem conserto; tem tratamento:
//
//   1. databaseType separa quem sofre do problema (weapon, armor) de quem não
//      sofre (consumíveis, cartas, materiais);
//   2. para equipamento, medianas de mínimos e máximos no lugar da média
//      (ver periodStats, em internal/web/stats.go);
//   3. os anúncios de AGORA revelam as faixas, porque um vazio grande entre
//      preços ordenados é o próprio mercado dizendo que ali há duas coisas
//      diferentes sendo vendidas;
//   4. a confiança é medida e exibida, e a PRECISÃO DA RESPOSTA ACOMPANHA A
//      CONFIANÇA DO DADO — um "~3 dias" calculado sobre dados contaminados é
//      uma mentira com cara de precisão.
//
// Tudo aqui é função pura sobre dados que o card já tem. Nenhuma requisição
// sai deste arquivo, e é por isso que mexer no seu preço, na lista de
// personagens ou na janela recalcula a tela inteira de graça.

// Quanto abaixo do concorrente mais barato fica o cenário de vender rápido.
const DESCONTO_VENDER_HOJE = 0.1;

// Quanto acima do mercado fica o cenário de segurar.
const ACRESCIMO_SEGURAR = 0.15;

// Um vazio entre dois preços ordenados só conta como fronteira de faixa se o
// preço de cima for pelo menos este tanto maior que o de baixo. Abaixo disso é
// variação normal de quem anuncia; acima, o mercado está partido.
const SALTO_DE_FAIXA = 3;

// Acima desta dispersão (teto ÷ chão do histórico) o item é tratado como
// contaminado: são coisas diferentes somadas na mesma série.
const DISPERSAO_SUSPEITA = 3;

// Abaixo deste volume na janela não há amostra para falar de velocidade.
const VOLUME_MINIMO = 3;

const CONFIANCA_ALTA = "alta";
const CONFIANCA_MEDIA = "media";
const CONFIANCA_BAIXA = "baixa";
const CONFIANCA_NENHUMA = "nenhuma";

// ehEquipamento marca os itens que aceitam refino e encantamento — os únicos
// em que uma unidade pode valer cem vezes outra com o mesmo nome. O site usa
// "armor" também para acessório e chapéu (ver isEquipment, no backend).
function ehEquipamento(item) {
  const tipo = item && item.databaseType;
  return tipo === "weapon" || tipo === "armor";
}

// detectarFaixas parte a lista de anúncios no maior vazio relativo.
//
// Não afirma POR QUE o mercado está partido — não temos como saber. Só mostra
// que está, o que já basta para o usuário se comparar com o grupo certo em vez
// de com a média de tudo. Custo zero: os anúncios já estão na tela.
//
// Devolve null quando não há partição defensável (menos de dois anúncios, ou
// nenhum salto grande o bastante).
function detectarFaixas(anuncios) {
  if (anuncios.length < 2) return null;

  let corte = -1;
  let maiorSalto = SALTO_DE_FAIXA;
  for (let i = 1; i < anuncios.length; i++) {
    const anterior = anuncios[i - 1].price;
    if (anterior <= 0) continue;
    const salto = anuncios[i].price / anterior;
    if (salto >= maiorSalto) {
      maiorSalto = salto;
      corte = i;
    }
  }
  if (corte === -1) return null;

  return {
    baixa: anuncios.slice(0, corte),
    alta: anuncios.slice(corte),
    salto: maiorSalto,
  };
}

// faixaDoSeuPreco diz em qual das faixas o seu anúncio cai — é com esse grupo
// que você compete de verdade. Sem preço definido ou sem partição, a
// concorrência é a lista inteira.
function faixaDoSeuPreco(anuncios, faixas, precoVenda) {
  if (!faixas || precoVenda == null) return anuncios;
  return precoVenda >= faixas.alta[0].price ? faixas.alta : faixas.baixa;
}

// avaliarConfianca resume o quanto dá para confiar nos números.
//
// É ela que decide a precisão de tudo que sai daqui: com confiança baixa, o
// card mostra a faixa e o motivo, e NÃO mostra preço sugerido — porque um
// número ali seria uma afirmação que o dado não sustenta.
function avaliarConfianca(item, concorrencia) {
  const resumo = item.historico && item.historico.summary;
  if (!resumo || resumo.days === 0 || resumo.qtySold < VOLUME_MINIMO) {
    return { nivel: CONFIANCA_NENHUMA, motivo: "Não há vendas registradas suficientes nesta janela." };
  }

  const disperso = resumo.dispersion >= DISPERSAO_SUSPEITA;
  if (ehEquipamento(item)) {
    // O histórico de um equipamento sempre soma refinos e encantamentos
    // diferentes. O que muda é se a mistura está atrapalhando de fato.
    if (disperso) {
      return {
        nivel: CONFIANCA_BAIXA,
        motivo:
          "Os preços deste equipamento variam " + Math.round(resumo.dispersion) +
          "× entre o mais barato e o mais caro. O histórico do site não distingue refino nem " +
          "encantamento, então esses números somam unidades bem diferentes.",
      };
    }
    return {
      nivel: CONFIANCA_MEDIA,
      motivo:
        "É um equipamento: o histórico não distingue refino nem encantamento, então os números " +
        "incluem unidades diferentes da sua.",
    };
  }

  if (disperso) {
    return {
      nivel: CONFIANCA_MEDIA,
      motivo: "Os preços registrados variam bastante nesta janela.",
    };
  }
  if (concorrencia.length === 0) {
    return {
      nivel: CONFIANCA_MEDIA,
      motivo: "Ninguém mais está anunciando este item, então não há com que comparar o preço de agora.",
    };
  }
  return { nivel: CONFIANCA_ALTA, motivo: "" };
}

// calcularLiquidez estima quantas unidades o mercado absorve por dia.
//
// Para equipamento, o volume do histórico é de TODAS as unidades do itemId,
// não só das que se parecem com a sua. A correção é escalar pela fatia do
// mercado que é comparável: se 2 dos 10 anúncios estão na sua faixa, assume-se
// que ~20% das vendas são dela. É uma suposição — que as vendas se dividem
// como os anúncios se dividem — e pode errar; mas é muito melhor que usar o
// volume total, e está dita em uma frase na tela.
function calcularLiquidez(item, concorrencia, todosOsAnuncios) {
  const resumo = item.historico && item.historico.summary;
  if (!resumo || resumo.days === 0 || resumo.qtySold <= 0) return 0;

  const porDia = resumo.qtySold / resumo.days;

  const unidadesTotais = somarUnidades(todosOsAnuncios);
  const unidadesDaFaixa = somarUnidades(concorrencia);
  if (unidadesTotais <= 0 || unidadesDaFaixa <= 0 || unidadesDaFaixa === unidadesTotais) {
    return porDia;
  }
  return porDia * (unidadesDaFaixa / unidadesTotais);
}

// filaNaFrente conta as unidades anunciadas mais baratas que a sua. A
// suposição embutida é que se compra do mais barato para o mais caro, o que é
// aproximadamente verdade no mercado do jogo.
//
// Recalculada a cada repintura do card, e não guardada: o mercado é dinâmico,
// e a fila de agora não é a de dez minutos atrás.
function filaNaFrente(concorrencia, preco) {
  if (preco == null) return 0;
  return somarUnidades(concorrencia.filter((a) => a.price < preco));
}

// posicaoNaFila é a sua colocação entre os anúncios, contando do mais barato.
//
// Diferente de tudo o mais neste arquivo, ISTO É UM FATO, não uma estimativa:
// é contagem de anúncios, não depende de liquidez, de histórico nem da
// confiança do dado. Por isso é mostrado sempre, exato, inclusive nos itens
// em que nenhum preço é recomendado — e é ele que dá resposta imediata a quem
// está mexendo no próprio preço para ver o que acontece.
function posicaoNaFila(concorrencia, preco) {
  if (preco == null) return null;
  const maisBaratos = concorrencia.filter((a) => a.price < preco).length;
  return { colocacao: maisBaratos + 1, total: concorrencia.length + 1 };
}

// distanciaDoMaisBarato é quanto o seu preço está acima (ou abaixo) do
// concorrente mais barato, em fração. Também um fato exato, e também o tipo de
// número que se move a cada zeny que o usuário digita.
function distanciaDoMaisBarato(concorrencia, preco) {
  if (preco == null || concorrencia.length === 0) return null;
  const maisBarato = concorrencia[0].price;
  if (maisBarato <= 0) return null;
  return (preco - maisBarato) / maisBarato;
}

// estimarTempo converte fila e liquidez em uma expectativa — e é aqui que a
// precisão acompanha a confiança. Um "~3 dias" calculado sobre dados
// contaminados soa exato e não é; uma faixa ou uma ordem de grandeza dizem a
// mesma coisa sem prometer o que não se sabe.
function estimarTempo(fila, porDia, confianca) {
  if (porDia <= 0 || confianca === CONFIANCA_NENHUMA) return "";
  if (fila === 0) return "você é o próximo da fila";

  const dias = fila / porDia;

  if (confianca === CONFIANCA_ALTA) {
    if (dias < 1) return "vende hoje";
    return "~" + Math.round(dias) + (Math.round(dias) === 1 ? " dia" : " dias");
  }
  if (confianca === CONFIANCA_MEDIA) {
    const piso = Math.max(1, Math.floor(dias * 0.6));
    const teto = Math.max(piso + 1, Math.ceil(dias * 1.6));
    return "entre " + piso + " e " + teto + " dias";
  }
  // Confiança baixa: só a ordem de grandeza.
  if (dias < 2) return "provavelmente rápido";
  if (dias < 14) return "provavelmente dias";
  if (dias < 60) return "provavelmente semanas";
  return "provavelmente meses";
}

// calcularTendencia compara a metade mais recente da janela com a anterior.
//
// Só devolve algo quando há dias suficientes e a variação é grande o bastante
// para não ser ruído com aparência de sinal.
const VARIACAO_MINIMA = 0.05;

function calcularTendencia(historico) {
  const dias = (historico && historico.days) || [];
  if (dias.length < 4) return null;

  // O site devolve do mais recente para o mais antigo.
  const meio = Math.floor(dias.length / 2);
  const recente = mediaSimples(dias.slice(0, meio));
  const anterior = mediaSimples(dias.slice(meio));
  if (anterior <= 0) return null;

  const variacao = (recente - anterior) / anterior;
  if (Math.abs(variacao) < VARIACAO_MINIMA) return null;
  return { variacao, subindo: variacao > 0 };
}

function mediaSimples(dias) {
  if (dias.length === 0) return 0;
  return dias.reduce((soma, d) => soma + d.avg, 0) / dias.length;
}

// calcularSugestao é a entrada única deste arquivo: junta tudo e devolve o que
// o card desenha. Sem efeito nenhum — só leitura do que já está na entrada.
function calcularSugestao(item) {
  const { meus, outros } = separarAnuncios(item.lastResult);
  const faixas = detectarFaixas(outros);
  const seuPreco = item.precoVenda;
  const concorrencia = faixaDoSeuPreco(outros, faixas, seuPreco);

  const confianca = avaliarConfianca(item, concorrencia);
  const porDia = calcularLiquidez(item, concorrencia, outros);
  const resumo = (item.historico && item.historico.summary) || null;

  const sugestao = {
    confianca,
    faixas,
    concorrencia,
    meus,
    tendencia: calcularTendencia(item.historico),
    porDia,
    faixa: null,
    cenarios: [],
    tempoNoSeuPreco: seuPreco != null ? estimarTempo(filaNaFrente(concorrencia, seuPreco), porDia, confianca.nivel) : "",
    posicao: posicaoNaFila(concorrencia, seuPreco),
    distancia: distanciaDoMaisBarato(concorrencia, seuPreco),
  };

  if (confianca.nivel === CONFIANCA_NENHUMA) return sugestao;

  const maisBarato = concorrencia.length > 0 ? concorrencia[0].price : 0;
  // Sem concorrência, o histórico é a única âncora: o chão e o teto medianos.
  const piso = maisBarato > 0 ? Math.round(maisBarato * (1 - DESCONTO_VENDER_HOJE)) : (resumo ? resumo.medianMin : 0);
  const teto = maisBarato > 0 ? Math.round(maisBarato * (1 + ACRESCIMO_SEGURAR)) : (resumo ? resumo.medianMax : 0);
  if (piso <= 0 || teto <= 0) return sugestao;

  sugestao.faixa = { min: Math.min(piso, teto), max: Math.max(piso, teto) };

  // Com confiança baixa o card mostra a faixa e o motivo, mas nenhum cenário:
  // recomendar um preço exato sobre dados que somam unidades diferentes seria
  // afirmar o que não se sabe.
  if (confianca.nivel === CONFIANCA_BAIXA || maisBarato <= 0) return sugestao;

  sugestao.cenarios = [
    {
      nome: "Vender hoje",
      preco: piso,
      expectativa: estimarTempo(filaNaFrente(concorrencia, piso), porDia, confianca.nivel),
      aposta:
        "Dez por cento abaixo do concorrente mais barato. Você passa para a frente da fila e " +
        "abre mão de parte do lucro para não ficar parado.",
    },
    {
      nome: "Preço de mercado",
      preco: Math.max(1, maisBarato - 1),
      expectativa: estimarTempo(filaNaFrente(concorrencia, maisBarato - 1), porDia, confianca.nivel),
      aposta:
        "Um zeny abaixo do mais barato de agora. Você entra na frente da fila atual, mas qualquer " +
        "anúncio novo mais barato passa na sua frente.",
    },
    {
      nome: "Segurar",
      preco: teto,
      expectativa: estimarTempo(filaNaFrente(concorrencia, teto), porDia, confianca.nivel),
      aposta:
        "Acima do mercado de agora. Só compensa se você estiver apostando em alta — uma " +
        "atualização do jogo que aumente a procura, ou o item ficando escasso. O custo da aposta " +
        "é o item ficar parado enquanto os mais baratos vendem.",
    },
  ];

  return sugestao;
}
