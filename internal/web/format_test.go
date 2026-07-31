package web

import "testing"

func TestNaviCommand(t *testing.T) {
	tests := []struct {
		name    string
		mapName string
		x, y    string
		want    string
	}{
		{
			// O detalhe da loja devolve o nome do arquivo de mapa; o comando
			// do jogo espera só o nome, sem a extensão.
			name:    "remove a extensão .gat",
			mapName: "prt_mk.gat",
			x:       "114", y: "180",
			want: "/navi prt_mk/114/180",
		},
		{
			name:    "nome já sem extensão passa direto",
			mapName: "prontera",
			x:       "1", y: "2",
			want: "/navi prontera/1/2",
		},
		{
			name:    "só o sufixo final é removido",
			mapName: "prt_gat.gat",
			x:       "0", y: "0",
			want: "/navi prt_gat/0/0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := naviCommand(tt.mapName, tt.x, tt.y); got != tt.want {
				t.Errorf("naviCommand = %q, quero %q", got, tt.want)
			}
		})
	}
}

func TestFormatMoney(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{
		{0, "0 z"},
		{7, "7 z"},
		{100, "100 z"},
		{1000, "1.000 z"},
		{59000, "59.000 z"},
		{1234567, "1.234.567 z"},
		{299999999, "299.999.999 z"},
		{-1500, "-1.500 z"},
	}

	for _, tt := range tests {
		if got := formatMoney(tt.in); got != tt.want {
			t.Errorf("formatMoney(%d) = %q, quero %q", tt.in, got, tt.want)
		}
	}
}

// TestFormatMoneyF arredonda antes de formatar porque a moeda do jogo não tem
// fração — é o que a média ponderada e o desvio padrão produzem.
func TestFormatMoneyF(t *testing.T) {
	tests := []struct {
		in   float64
		want string
	}{
		{0, "0 z"},
		{1750, "1.750 z"},
		{829.156, "829 z"},
		{829.5, "830 z"},
		{1749.4, "1.749 z"},
	}

	for _, tt := range tests {
		if got := formatMoneyF(tt.in); got != tt.want {
			t.Errorf("formatMoneyF(%v) = %q, quero %q", tt.in, got, tt.want)
		}
	}
}
