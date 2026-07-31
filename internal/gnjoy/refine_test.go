package gnjoy

import "testing"

func TestParseRefine(t *testing.T) {
	tests := []struct {
		itemFullName string
		want         int
	}{
		{"", 0},
		{"Pó de Éter", 0},
		{"+7Laço da Celine[1]", 7},
		{"+10Espada Primordial[2]", 10},
		// "+0" não aparece na prática (o site omite o prefixo em itens sem
		// refino), mas se aparecer o resultado tem de ser o mesmo: 0.
		{"+0Espada", 0},
		// Sem dígito após o "+", não é um prefixo de refino.
		{"+Espada", 0},
		// O prefixo só conta no início do nome.
		{"Espada +7", 0},
		// Número grande demais para caber em um int: melhor tratar como sem
		// refino do que estourar.
		{"+99999999999999999999999Espada", 0},
	}

	for _, tt := range tests {
		t.Run(tt.itemFullName, func(t *testing.T) {
			if got := parseRefine(tt.itemFullName); got != tt.want {
				t.Errorf("parseRefine(%q) = %d, quero %d", tt.itemFullName, got, tt.want)
			}
		})
	}
}

// TestStoreDetailUnmarshalCalculaRefine garante que o refino é derivado no
// momento da decodificação: nenhum chamador precisa lembrar de invocar
// parseRefine manualmente.
func TestStoreDetailUnmarshalCalculaRefine(t *testing.T) {
	var detail StoreDetail
	raw := []byte(`{"ssi":"abc","itemFullName":"+11Laço da Celine[1]","itemPrice":500}`)
	if err := detail.UnmarshalJSON(raw); err != nil {
		t.Fatalf("UnmarshalJSON: %v", err)
	}
	if detail.Refine != 11 {
		t.Errorf("Refine = %d, quero 11", detail.Refine)
	}
	if detail.ItemFullName != "+11Laço da Celine[1]" {
		t.Errorf("ItemFullName = %q, o nome original precisa ser preservado", detail.ItemFullName)
	}
	if detail.ItemPrice != 500 {
		t.Errorf("ItemPrice = %d, quero 500", detail.ItemPrice)
	}
}

func TestStoreDetailUnmarshalJSONInvalido(t *testing.T) {
	var detail StoreDetail
	if err := detail.UnmarshalJSON([]byte(`{"itemPrice":"não é número"}`)); err == nil {
		t.Fatal("esperava erro, veio nil")
	}
}
