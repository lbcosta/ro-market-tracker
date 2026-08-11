package telegram

import (
	"bufio"
	"os"
	"strings"
)

// Config são as credenciais lidas do arquivo de configuração (ver
// LoadConfigFile).
type Config struct {
	BotToken string
	ChatID   string
}

// Configured diz se as duas chaves foram informadas. Sem as duas, a
// integração fica desligada — é o estado padrão de quem baixou a release e
// ainda não editou o arquivo (ver cmd/server/main.go).
func (c Config) Configured() bool {
	return c.BotToken != "" && c.ChatID != ""
}

// LoadConfigFile lê TELEGRAM_BOT_TOKEN e TELEGRAM_CHAT_ID de um arquivo no
// formato CHAVE=valor, uma por linha — linhas vazias e começando com "#" são
// ignoradas, para o arquivo poder vir com instruções em comentário.
//
// Um arquivo ausente não é erro: é o estado padrão de quem baixou a release
// e ainda não configurou nada. Outros erros de leitura (permissão, etc.)
// voltam para quem chamou decidir o que fazer.
func LoadConfigFile(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, nil
		}
		return Config{}, err
	}
	defer f.Close()

	var cfg Config
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "TELEGRAM_BOT_TOKEN":
			cfg.BotToken = strings.TrimSpace(value)
		case "TELEGRAM_CHAT_ID":
			cfg.ChatID = strings.TrimSpace(value)
		}
	}
	if err := scanner.Err(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
