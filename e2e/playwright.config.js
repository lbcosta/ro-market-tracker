const { defineConfig, devices } = require("@playwright/test");
const { APP_URL } = require("./config");

module.exports = defineConfig({
  testDir: "./tests",
  globalSetup: require.resolve("./global-setup"),

  // O servidor sob teste tem um rate limiter de verdade (é o ponto), então
  // rodar specs em paralelo faria uns testes esperarem a fila dos outros e
  // deixaria as asserções de tempo instáveis.
  workers: 1,
  fullyParallel: false,

  // A watchlist e a barra de atividades vivem em localStorage e em um stream
  // SSE; um teste que falha por corrida costuma passar na repetição, então
  // repetir localmente esconderia problemas reais.
  retries: 0,
  reporter: process.env.CI ? "list" : [["list"], ["html", { open: "never" }]],

  timeout: 30_000,
  globalTimeout: 10 * 60_000,

  use: {
    baseURL: APP_URL,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    // A watchlist pede permissão de notificação no primeiro alvo atingido;
    // concedendo de antemão, requestPermission() resolve na hora em vez de
    // deixar o teste pendurado num diálogo que ninguém vai responder.
    permissions: ["notifications", "clipboard-read", "clipboard-write"],
  },

  projects: [
    {
      name: "chromium",
      use: {
        ...devices["Desktop Chrome"],
        // "chromium" usa o build completo do navegador em vez do
        // headless shell, que depende de menos bibliotecas do sistema e é o
        // que funciona em containers enxutos.
        channel: "chromium",
        // O sandbox do Chromium precisa de user namespaces, que boa parte
        // dos containers de CI não expõe. A única página carregada aqui é a
        // do próprio servidor em localhost, então desligar é seguro.
        chromiumSandbox: false,
      },
    },
  ],
});
