package telegram

import (
	"os"
	"path/filepath"
	"testing"
)

func escreverArquivo(t *testing.T, conteudo string) string {
	t.Helper()
	caminho := filepath.Join(t.TempDir(), "telegram.txt")
	if err := os.WriteFile(caminho, []byte(conteudo), 0o644); err != nil {
		t.Fatal(err)
	}
	return caminho
}

func TestLoadConfigFileLeAsDuasChaves(t *testing.T) {
	caminho := escreverArquivo(t, "TELEGRAM_BOT_TOKEN=abc123\nTELEGRAM_CHAT_ID=999\n")

	cfg, err := LoadConfigFile(caminho)
	if err != nil {
		t.Fatalf("LoadConfigFile retornou erro: %v", err)
	}
	if cfg.BotToken != "abc123" || cfg.ChatID != "999" {
		t.Errorf("cfg = %+v, esperado BotToken=abc123 ChatID=999", cfg)
	}
	if !cfg.Configured() {
		t.Error("Configured() = false, esperado true")
	}
}

// Comentários e linhas em branco existem para o arquivo poder vir com
// instruções de como preencher (ver docs/telegram.txt) sem que isso vire
// uma chave desconhecida.
func TestLoadConfigFileIgnoraComentariosELinhasEmBranco(t *testing.T) {
	caminho := escreverArquivo(t, "# comentário\n\nTELEGRAM_BOT_TOKEN=abc123\n\n# outro comentário\nTELEGRAM_CHAT_ID=999\n")

	cfg, err := LoadConfigFile(caminho)
	if err != nil {
		t.Fatalf("LoadConfigFile retornou erro: %v", err)
	}
	if cfg.BotToken != "abc123" || cfg.ChatID != "999" {
		t.Errorf("cfg = %+v, esperado BotToken=abc123 ChatID=999", cfg)
	}
}

// É o estado padrão de quem baixou a release e ainda não editou o arquivo —
// não pode ser erro.
func TestLoadConfigFileArquivoAusenteNaoEErro(t *testing.T) {
	caminho := filepath.Join(t.TempDir(), "não-existe.txt")

	cfg, err := LoadConfigFile(caminho)
	if err != nil {
		t.Fatalf("LoadConfigFile retornou erro: %v", err)
	}
	if cfg.Configured() {
		t.Errorf("cfg = %+v, esperado vazio (não configurado)", cfg)
	}
}

func TestLoadConfigFileComSoUmaChaveNaoEstaConfigurado(t *testing.T) {
	caminho := escreverArquivo(t, "TELEGRAM_BOT_TOKEN=abc123\n")

	cfg, err := LoadConfigFile(caminho)
	if err != nil {
		t.Fatalf("LoadConfigFile retornou erro: %v", err)
	}
	if cfg.Configured() {
		t.Errorf("cfg = %+v, esperado não configurado com só uma chave preenchida", cfg)
	}
}
