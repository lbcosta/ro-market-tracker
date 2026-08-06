// version.js: mostra a versão do binário rodando (já vem pronta no HTML,
// injetada em tempo de build — ver main.version em cmd/server) e avisa,
// discretamente, quando existe uma release mais nova publicada no GitHub.
//
// A checagem acontece só no navegador, direto na API pública do GitHub — não
// passa pelo servidor Go nem pelo rate limiter do gnjoy.Client, porque não
// tem nada a ver com o site do jogo. O repositório é público, e a API do
// GitHub responde com CORS liberado para leitura anônima; não há chave nem
// autenticação envolvida.

const GITHUB_RELEASES_LATEST_URL = "https://api.github.com/repos/lbcosta/ro-market-tracker/releases/latest";

// VERSION_CHECK_CACHE_KEY guarda a última resposta por um tempo, para
// recarregar a página com frequência não gastar a cota anônima da API do
// GitHub (60 requisições/hora por IP) só com isto. Novidade de release não é
// urgente — seis horas de atraso para descobrir uma atualização não custa
// nada a ninguém.
const VERSION_CHECK_CACHE_KEY = "ro-market-tracker:latest-release-check";
const VERSION_CHECK_TTL_MS = 6 * 60 * 60 * 1000;

// parseVersion lê "vMAJOR.MINOR.PATCH" (o "v" é opcional, e qualquer sufixo
// depois dos três números — "-rc1", por exemplo — é ignorado) e devolve os
// três números para comparação numérica. Devolve null para o que não casar,
// principalmente "dev": um build local, sem tag, não tem com o que comparar.
function parseVersion(v) {
  const m = /^v?(\d+)\.(\d+)\.(\d+)/.exec(String(v || "").trim());
  if (!m) return null;
  return [Number(m[1]), Number(m[2]), Number(m[3])];
}

// versionMenorQue compara major.minor.patch em ordem — não dá para comparar
// as duas versões como texto (v0.9.0 viria "maior" que v0.10.0 char a char).
function versionMenorQue(a, b) {
  for (let i = 0; i < 3; i++) {
    if (a[i] !== b[i]) return a[i] < b[i];
  }
  return false;
}

// buscarUltimaRelease devolve a tag da última release (ex.: "v0.8.0"), do
// cache local se ainda válido, ou da API do GitHub — e null se a consulta
// falhar (sem internet, GitHub fora do ar, ou a cota anônima esgotada).
async function buscarUltimaRelease() {
  try {
    const cache = JSON.parse(localStorage.getItem(VERSION_CHECK_CACHE_KEY) || "null");
    if (cache && Date.now() - cache.fetchedAt < VERSION_CHECK_TTL_MS) {
      return cache.tagName;
    }
  } catch {
    // localStorage indisponível ou com lixo salvo: segue para a consulta.
  }

  let tagName;
  try {
    const resp = await fetch(GITHUB_RELEASES_LATEST_URL, {
      headers: { Accept: "application/vnd.github+json" },
    });
    if (!resp.ok) return null;
    tagName = (await resp.json()).tag_name;
  } catch {
    return null;
  }

  try {
    localStorage.setItem(VERSION_CHECK_CACHE_KEY, JSON.stringify({ tagName, fetchedAt: Date.now() }));
  } catch {
    // Sem persistência: a próxima carga consulta de novo, sem problema — é
    // só uma otimização de cota, não uma exigência.
  }
  return tagName;
}

// checarAtualizacao é silenciosa em qualquer caminho que não seja "existe
// uma versão mais nova, de verdade": isto é um aviso de conveniência, não
// vale incomodar por uma falha de rede, nem por estar rodando um build local
// sem tag ("dev"), nem por já estar em dia.
async function checarAtualizacao() {
  const atualTexto = document.getElementById("version-current")?.textContent;
  const atual = parseVersion(atualTexto);
  if (!atual) return;

  const ultimaTexto = await buscarUltimaRelease();
  const ultima = parseVersion(ultimaTexto);
  if (!ultima || !versionMenorQue(atual, ultima)) return;

  const link = document.getElementById("version-update-link");
  if (!link) return;
  link.hidden = false;
  link.title = "Versão " + ultimaTexto + " disponível — você está na " + atualTexto;
}

document.addEventListener("DOMContentLoaded", checarAtualizacao);
