// theme.js alterna manualmente entre os temas claro e escuro, por cima da
// preferência do sistema (que continua valendo até a primeira troca). A
// escolha fica em localStorage; o script inline em index.html.tmpl já a
// aplica antes da primeira pintura, para não piscar o tema errado a cada
// carregamento.
const THEME_KEY = "ro-market-tracker:theme";

function temaAtual() {
  const escolhido = document.documentElement.dataset.theme;
  if (escolhido === "light" || escolhido === "dark") return escolhido;
  return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
}

function aplicarTema(tema) {
  document.documentElement.dataset.theme = tema;
  try {
    localStorage.setItem(THEME_KEY, tema);
  } catch {
    // localStorage pode estar indisponível (modo privado); a troca ainda
    // funciona na sessão atual, só não sobrevive a um recarregamento.
  }
}

document.getElementById("theme-toggle")?.addEventListener("click", () => {
  aplicarTema(temaAtual() === "dark" ? "light" : "dark");
});
