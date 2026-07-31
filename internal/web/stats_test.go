package web

import (
	"math"
	"testing"

	"github.com/lbcosta/ro-market-tracker/internal/gnjoy"
)

func TestComputeSevenDayStats(t *testing.T) {
	// Os mesmos números das fixtures do mock, escolhidos para que a conta
	// feche em valores exatos:
	//   qtd total = 1+1+2 = 4
	//   média ponderada = (2000*1 + 3000*1 + 1000*2) / 4 = 7000/4 = 1750
	//   variância = (1*250² + 1*1250² + 2*750²) / 4 = 2750000/4 = 687500
	days := []gnjoy.PriceDayStat{
		{MinItemPrice: 1000, MaxItemPrice: 3000, AvgItemPrice: 2000, ItemCnt: 1},
		{MinItemPrice: 2000, MaxItemPrice: 4000, AvgItemPrice: 3000, ItemCnt: 1},
		{MinItemPrice: 500, MaxItemPrice: 5000, AvgItemPrice: 1000, ItemCnt: 2},
	}

	stats := computeSevenDayStats(days)

	if stats.Days != 3 {
		t.Errorf("Days = %d, quero 3", stats.Days)
	}
	if stats.Min != 500 {
		t.Errorf("Min = %d, quero 500", stats.Min)
	}
	if stats.Max != 5000 {
		t.Errorf("Max = %d, quero 5000", stats.Max)
	}
	if stats.QtySold != 4 {
		t.Errorf("QtySold = %d, quero 4", stats.QtySold)
	}
	if stats.WeightedAvg != 1750 {
		t.Errorf("WeightedAvg = %v, quero 1750", stats.WeightedAvg)
	}
	if want := math.Sqrt(687500); math.Abs(stats.StdDev-want) > 1e-9 {
		t.Errorf("StdDev = %v, quero %v", stats.StdDev, want)
	}
}

// TestComputeSevenDayStatsPonderaPelaQuantidade é o ponto do cálculo: como o
// site só devolve a média de cada dia (e não cada venda), um dia com muitas
// vendas precisa pesar mais do que um dia com uma venda só — uma média simples
// entre os dias distorceria o resultado.
func TestComputeSevenDayStatsPonderaPelaQuantidade(t *testing.T) {
	days := []gnjoy.PriceDayStat{
		{MinItemPrice: 100, MaxItemPrice: 100, AvgItemPrice: 100, ItemCnt: 99},
		{MinItemPrice: 1000, MaxItemPrice: 1000, AvgItemPrice: 1000, ItemCnt: 1},
	}

	stats := computeSevenDayStats(days)

	// Média simples seria 550; ponderada é (100*99 + 1000*1)/100 = 109.
	if stats.WeightedAvg != 109 {
		t.Errorf("WeightedAvg = %v, quero 109 (ponderada, não a média simples 550)", stats.WeightedAvg)
	}
}

func TestComputeSevenDayStatsSemDias(t *testing.T) {
	stats := computeSevenDayStats(nil)

	if stats != (sevenDayStats{}) {
		t.Errorf("stats = %+v, quero tudo zerado", stats)
	}
}

func TestComputeSevenDayStatsUmDia(t *testing.T) {
	stats := computeSevenDayStats([]gnjoy.PriceDayStat{
		{MinItemPrice: 700, MaxItemPrice: 900, AvgItemPrice: 800, ItemCnt: 5},
	})

	if stats.Days != 1 || stats.Min != 700 || stats.Max != 900 {
		t.Errorf("stats = %+v, quero 1 dia com faixa 700..900", stats)
	}
	if stats.WeightedAvg != 800 {
		t.Errorf("WeightedAvg = %v, quero 800", stats.WeightedAvg)
	}
	// Com um único ponto não há dispersão.
	if stats.StdDev != 0 {
		t.Errorf("StdDev = %v, quero 0", stats.StdDev)
	}
}

// TestComputeSevenDayStatsSemQuantidade cobre o caso em que o site devolve
// dias com preço mas com itemCnt zerado: dá para mostrar a faixa de preço,
// mas dividir por zero para achar a média não faria sentido.
func TestComputeSevenDayStatsSemQuantidade(t *testing.T) {
	stats := computeSevenDayStats([]gnjoy.PriceDayStat{
		{MinItemPrice: 300, MaxItemPrice: 900, AvgItemPrice: 600, ItemCnt: 0},
		{MinItemPrice: 200, MaxItemPrice: 400, AvgItemPrice: 300, ItemCnt: 0},
	})

	if stats.Days != 2 {
		t.Errorf("Days = %d, quero 2", stats.Days)
	}
	if stats.Min != 200 || stats.Max != 900 {
		t.Errorf("faixa = %d..%d, quero 200..900", stats.Min, stats.Max)
	}
	if stats.QtySold != 0 {
		t.Errorf("QtySold = %d, quero 0", stats.QtySold)
	}
	if stats.WeightedAvg != 0 || stats.StdDev != 0 {
		t.Errorf("média/desvio = %v/%v, quero 0/0 sem quantidade para ponderar", stats.WeightedAvg, stats.StdDev)
	}
}
