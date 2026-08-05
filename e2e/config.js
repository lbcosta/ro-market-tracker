// Portas fixas usadas pelos testes de navegador. São fixas (em vez de
// sorteadas) porque a configuração do Playwright é avaliada antes do
// globalSetup rodar, então o baseURL precisa ser conhecido de antemão.
const APP_PORT = 8790;
const MOCK_PORT = 8791;

const APP_URL = `http://127.0.0.1:${APP_PORT}`;
const MOCK_URL = `http://127.0.0.1:${MOCK_PORT}`;

// Ritmo do rate limiter do servidor durante os testes: rápido o bastante para
// a suíte não demorar, lento o bastante para que a fila seja observável (é o
// que faz o cronômetro da barra de atividades aparecer). Com burst 1, duas
// requisições seguidas ficam a 500 ms de distância.
const RATE_LIMIT_RPS = "2";
const RATE_LIMIT_BURST = "1";

// De quanto em quanto tempo o servidor verifica se o site voltou a aceitar
// consultas depois de um 429 (o padrão são dez minutos). Curto aqui porque a
// suspensão é global ao processo e a suíte compartilha um servidor: sem isso,
// um teste de bloqueio deixaria todos os seguintes travados.
const SUSPENSION_PROBE_INTERVAL = "1s";

module.exports = {
  APP_PORT,
  MOCK_PORT,
  APP_URL,
  MOCK_URL,
  RATE_LIMIT_RPS,
  RATE_LIMIT_BURST,
  SUSPENSION_PROBE_INTERVAL,
};
