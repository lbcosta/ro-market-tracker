const { test, expect } = require("@playwright/test");
const { resetPage } = require("./helpers");

// A checagem de atualização fala direto com a API do GitHub, não com o
// servidor Go — nenhum teste aqui deve tocar a API de verdade (o mesmo
// princípio já aplicado ao GnJoy: flaky, depende de internet, sujeito a
// limite de requisições). Toda resposta é interceptada.
const GITHUB_LATEST_URL = "https://api.github.com/repos/lbcosta/ro-market-tracker/releases/latest";

function mockarUltimaRelease(page, tagName) {
  return page.route(GITHUB_LATEST_URL, (route) =>
    route.fulfill({ contentType: "application/json", body: JSON.stringify({ tag_name: tagName }) }),
  );
}

test.beforeEach(async ({ page, request }) => {
  await resetPage(page, request);
});

test("mostra a versão em execução no canto", async ({ page }) => {
  // O binário de teste é compilado sem -ldflags, então a versão é sempre
  // "dev" (ver main.version em cmd/server) — o mesmo valor de quem roda
  // "go run ./cmd/server" localmente, sem baixar uma release.
  await expect(page.locator("#version-current")).toHaveText("dev");
  await expect(page.locator("#version-update-link")).toBeHidden();
});

// "dev" não é uma versão com tag: não há com o que compará-la, e o aviso de
// atualização não pode aparecer para quem está rodando um build local.
test("build de desenvolvimento nunca mostra aviso de atualização", async ({ page }) => {
  await mockarUltimaRelease(page, "v99.0.0");
  await page.evaluate(() => checarAtualizacao());

  await expect(page.locator("#version-update-link")).toBeHidden();
});

// A versão real de teste é sempre "dev" (não comparável), então o cenário
// "existe uma release mais nova" é exercitado com uma versão atual simulada
// — a mesma técnica de checarAtualizacao() já usa a função global
// diretamente, e não depende de nenhum binário com tag de verdade.
test("com uma versão mais nova publicada, mostra o link de atualização", async ({ page }) => {
  await mockarUltimaRelease(page, "v9.9.9");
  await page.evaluate(() => {
    document.getElementById("version-current").textContent = "v1.0.0";
  });

  await page.evaluate(() => checarAtualizacao());

  const link = page.locator("#version-update-link");
  await expect(link).toBeVisible();
  await expect(link).toHaveAttribute("href", /releases\/latest/);
  await expect(link).toHaveAttribute("title", /v9\.9\.9/);
});

test("já na versão mais nova, não mostra o link de atualização", async ({ page }) => {
  await mockarUltimaRelease(page, "v1.0.0");
  await page.evaluate(() => {
    document.getElementById("version-current").textContent = "v1.0.0";
  });

  await page.evaluate(() => checarAtualizacao());

  await expect(page.locator("#version-update-link")).toBeHidden();
});

// v0.9.0 é "maior" que v0.10.0 se comparado como texto — a comparação
// precisa ser numérica, campo a campo.
test("compara major.minor.patch numericamente, não como texto", async ({ page }) => {
  await mockarUltimaRelease(page, "v0.10.0");
  await page.evaluate(() => {
    document.getElementById("version-current").textContent = "v0.9.0";
  });

  await page.evaluate(() => checarAtualizacao());

  await expect(page.locator("#version-update-link")).toBeVisible();
});

// A cota anônima da API do GitHub é de 60 requisições/hora por IP — recarregar
// a página não pode custar uma consulta nova a cada vez.
test("a resposta fica em cache por um tempo, sem repetir a consulta", async ({ page }) => {
  let chamadas = 0;
  await page.route(GITHUB_LATEST_URL, (route) => {
    chamadas++;
    return route.fulfill({ contentType: "application/json", body: JSON.stringify({ tag_name: "v1.0.0" }) });
  });

  // O texto precisa ser refeito a cada chamada: um recarregamento renderiza
  // "dev" de novo (é o servidor quem escreve #version-current), e é só o
  // cache em localStorage — não o texto na tela — que precisa sobreviver ao
  // reload para este teste fazer sentido.
  const forcarVersaoEChecar = () =>
    page.evaluate(() => {
      document.getElementById("version-current").textContent = "v1.0.0";
      return checarAtualizacao();
    });

  await forcarVersaoEChecar();
  await forcarVersaoEChecar();
  await page.reload();
  await forcarVersaoEChecar();

  expect(chamadas).toBe(1);
});

// Uma falha de rede (ou o GitHub fora do ar, ou a cota anônima esgotada) não
// pode quebrar nada nem incomodar quem está usando o programa — é só um
// aviso de conveniência.
test("uma falha ao consultar o GitHub não mostra nada e não quebra a página", async ({ page }) => {
  await page.route(GITHUB_LATEST_URL, (route) => route.abort());
  await page.evaluate(() => {
    document.getElementById("version-current").textContent = "v1.0.0";
  });

  await page.evaluate(() => checarAtualizacao());

  await expect(page.locator("#version-update-link")).toBeHidden();
  // A página continua funcionando normalmente.
  await expect(page.locator("h1")).toHaveText("RO Market Tracker");
});
