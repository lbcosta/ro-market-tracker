# Testes de navegador

Testes ponta a ponta do frontend, dirigindo um Chromium de verdade contra o
servidor real (`cmd/server`).

## Nada aqui toca a API do GnJoy LATAM

O `globalSetup` sobe dois processos antes da suíte:

1. **`internal/gnjoytest/cmd/mockgnjoy`** — o site falso do GnJoy, que fala o
   mesmo protocolo das rotas internas reais (RSC Flight, Server Actions e a
   descoberta do action id nos chunks JS).
2. **`cmd/server`** — o servidor de verdade, com `GNJOY_BASE_URL` apontando
   para o mock.

O site real tem rate limiting próprio e não é uma API pública documentada:
uma suíte batendo nele seria frágil e um jeito rápido de tomar bloqueio. O
mesmo mock é usado pelos testes de Go (`go test ./...`), então as duas suítes
concordam sobre o que o upstream faz.

O servidor de teste roda com um rate limit de 2 req/s e burst 1 (ver
`config.js`): rápido o bastante para a suíte não demorar, lento o bastante
para que a fila do rate limiter seja observável — é o que permite testar o
cronômetro da barra de atividades.

## Rodando

```sh
cd e2e
npm install
npx playwright install --with-deps chromium   # só na primeira vez
npm test
```

Não é preciso subir nada à mão: o `globalSetup` compila e sobe o mock e o
servidor, e os derruba no fim.

Outros comandos:

```sh
npm run test:headed          # com o navegador visível
npx playwright test busca    # só um arquivo
npm run report               # abre o relatório HTML da última execução
```

### Se todo teste falhar no lançamento do navegador

```
browserType.launch: Target page, context or browser has been closed
[err] .../chrome: error while loading shared libraries: libnspr4.so: cannot
open shared object file: No such file or directory
```

Isso não é problema da suíte: o Chromium foi baixado, mas as bibliotecas de
sistema de que ele depende não estão instaladas — o que acontece quando o
`playwright install` roda **sem** o `--with-deps` da receita acima (esse é o
trecho que precisa de root, e é fácil ele ficar de fora). O erro cita só a
primeira biblioteca que faltou; para ver todas:

```sh
ldd ~/.cache/ms-playwright/chromium-*/chrome-linux64/chrome | grep "not found"
```

No Ubuntu 24.04 basta instalar os pacotes que as fornecem:

```sh
sudo apt-get install -y libnss3 libnspr4 libasound2t64
```

O equivalente genérico é `sudo npx playwright install-deps chromium`, que
instala o conjunto completo recomendado pelo Playwright (bem maior) sem você
precisar descobrir os nomes dos pacotes.

## Plano de controle do mock

O binário do mock expõe rotas sob `/__mock/` que os testes usam para provocar
cenários que não dá para reproduzir só pela interface (ver `tests/helpers.js`):

| Rota | O que faz |
|------|-----------|
| `POST /__mock/fail?status=&times=&retryAfter=` | Faz as próximas `times` requisições falharem |
| `POST /__mock/delay?ms=&times=` | Atrasa as próximas `times` respostas sem falhá-las |
| `GET /__mock/requests` | Quantas requisições o upstream recebeu |
| `POST /__mock/reset` | Zera o contador e descarta falhas/atrasos pendentes |

O `reset` é chamado antes de cada teste: sem isso, uma falha enfileirada e não
consumida sobraria para o teste seguinte e o derrubaria por um motivo sem
relação nenhuma com ele.

## Sobre a isolação entre testes

Dois estados sobrevivem entre os testes e precisam de atenção ao escrever
casos novos:

- **A watchlist** vive em `localStorage`; o `resetPage` limpa.
- **O log de atividade** é do servidor e acumula desde que ele subiu. Por
  isso as asserções da barra são sobre a ordem e o conteúdo das linhas, nunca
  sobre a quantidade total.
