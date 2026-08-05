const { execFileSync, spawn } = require("node:child_process");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");

const {
  APP_PORT,
  MOCK_PORT,
  APP_URL,
  MOCK_URL,
  RATE_LIMIT_RPS,
  RATE_LIMIT_BURST,
  SUSPENSION_PROBE_INTERVAL,
} = require("./config");

const repoRoot = path.resolve(__dirname, "..");

// globalSetup sobe a pilha inteira antes da suíte: o site falso do GnJoy
// (internal/gnjoytest) e o servidor real apontado para ele. Nenhum teste toca
// a API de verdade — o site do GnJoy LATAM tem rate limiting próprio e não é
// uma API pública, então uma suíte batendo nele seria frágil e um jeito rápido
// de tomar bloqueio.
//
// O Playwright chama a função devolvida daqui como teardown.
module.exports = async () => {
  const binDir = fs.mkdtempSync(path.join(os.tmpdir(), "ro-market-tracker-e2e-"));

  // Compilar uma vez e rodar o binário (em vez de "go run") dá um processo
  // filho direto, que dá para matar de forma confiável no teardown.
  const mockBin = path.join(binDir, "mockgnjoy");
  const serverBin = path.join(binDir, "server");
  build("./internal/gnjoytest/cmd/mockgnjoy", mockBin);
  build("./cmd/server", serverBin);

  const mock = spawn(mockBin, ["-addr", `127.0.0.1:${MOCK_PORT}`], {
    cwd: repoRoot,
    stdio: ["ignore", "pipe", "pipe"],
  });
  const actionID = await readActionID(mock);

  const server = spawn(serverBin, [], {
    cwd: repoRoot,
    stdio: ["ignore", "pipe", "pipe"],
    env: {
      ...process.env,
      PORT: String(APP_PORT),
      GNJOY_BASE_URL: MOCK_URL,
      // Sem isto, a primeira Server Action bateria com o hash do site real,
      // levaria 404 e gastaria uma rodada inteira de redescoberta antes de
      // qualquer teste começar.
      GNJOY_ACTION_ID: actionID,
      GNJOY_RATE_LIMIT_RPS: RATE_LIMIT_RPS,
      GNJOY_RATE_LIMIT_BURST: RATE_LIMIT_BURST,
      // O padrão são dez minutos, e a suspensão é global ao processo: um
      // teste que a dispare deixaria todos os seguintes bloqueados (a suíte
      // roda com um servidor só e um worker só). Encurtando aqui, a
      // recuperação é o caminho REAL sendo exercitado — não uma rota de
      // escape que só existiria para os testes.
      GNJOY_SUSPENSION_PROBE_INTERVAL: SUSPENSION_PROBE_INTERVAL,
    },
  });
  pipeOnFailure(server, "server");

  await waitForHealth(`${APP_URL}/healthz`);

  return async () => {
    server.kill("SIGTERM");
    mock.kill("SIGTERM");
    fs.rmSync(binDir, { recursive: true, force: true });
  };
};

function build(pkg, out) {
  try {
    execFileSync("go", ["build", "-o", out, pkg], { cwd: repoRoot, stdio: "pipe" });
  } catch (err) {
    const detalhe = err.stderr ? err.stderr.toString() : err.message;
    throw new Error(`falha ao compilar ${pkg}:\n${detalhe}`);
  }
}

// readActionID espera o mock anunciar em qual porta subiu e com qual hash de
// Server Action, para o servidor não precisar adivinhar nenhum dos dois.
function readActionID(mock) {
  return new Promise((resolve, reject) => {
    let saida = "";
    const timer = setTimeout(() => {
      reject(new Error(`o mock não subiu em 10s. Saída até agora:\n${saida}`));
    }, 10_000);

    mock.stdout.on("data", (chunk) => {
      saida += chunk.toString();
      const match = saida.match(/mockgnjoy action id (\S+)/);
      if (match) {
        clearTimeout(timer);
        resolve(match[1]);
      }
    });
    mock.stderr.on("data", (chunk) => {
      saida += chunk.toString();
    });
    mock.on("exit", (code) => {
      clearTimeout(timer);
      reject(new Error(`o mock encerrou com código ${code} antes de subir. Saída:\n${saida}`));
    });
  });
}

// pipeOnFailure guarda a saída do processo e só a despeja se ele morrer — um
// servidor que cai calado deixa a suíte inteira falhando sem explicação.
function pipeOnFailure(proc, nome) {
  let saida = "";
  proc.stdout.on("data", (c) => (saida += c.toString()));
  proc.stderr.on("data", (c) => (saida += c.toString()));
  proc.on("exit", (code, signal) => {
    if (signal === "SIGTERM") return; // encerramento normal do teardown
    console.error(`[${nome}] encerrou com código ${code}. Saída:\n${saida}`);
  });
}

async function waitForHealth(url) {
  const prazo = Date.now() + 20_000;
  let ultimoErro;
  while (Date.now() < prazo) {
    try {
      const resp = await fetch(url);
      if (resp.ok) return;
      ultimoErro = new Error(`status ${resp.status}`);
    } catch (err) {
      ultimoErro = err;
    }
    await new Promise((r) => setTimeout(r, 100));
  }
  throw new Error(`o servidor não respondeu em ${url}: ${ultimoErro}`);
}
