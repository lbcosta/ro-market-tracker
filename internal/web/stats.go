package web

import (
	"math"
	"slices"

	"github.com/lbcosta/ro-market-tracker/internal/gnjoy"
)

// periodStats resume o comportamento de preço de um item numa janela de dias
// — quantos vierem, conforme o Limit pedido ao site —, a partir
// dos agregados diários que o próprio GnJoy Americas já calcula. Não temos o
// preço de cada transação individual, só min/média/máx por dia — por isso
// a média e o desvio padrão aqui são ponderados pela quantidade negociada
// em cada dia (ItemCnt), o que é mais fiel ao real do que uma média simples
// entre os dias.
type periodStats struct {
	Days        int
	Min         int64
	Max         int64
	WeightedAvg float64
	StdDev      float64
	QtySold     int

	// MedianMin e MedianMax são as MEDIANAS dos mínimos e dos máximos
	// diários, e existem por causa de um defeito do dado de origem: o
	// histórico é indexado por itemId, mas refino e encantamento são
	// propriedades da UNIDADE. Uma bota +0 e uma bota +9 com encantamento
	// raro são o mesmo itemId, então a série diária não é uma distribuição —
	// são várias empilhadas.
	//
	// Nessa mistura, a média cai no vazio entre os grupos e não descreve
	// nada. O mínimo diário é o mais próximo que se chega da versão comum do
	// item; o máximo, do que uma unidade boa alcança. E mediana, não média,
	// porque um único dia de 88kk destrói uma média e mal move uma mediana.
	MedianMin int64
	MedianMax int64

	// Dispersao é MedianMax dividido por MedianMin: quantas vezes o teto é
	// maior que o chão. É o indicador de o quanto a mistura acima está
	// atrapalhando — perto de 1, o item é homogêneo e o histórico é
	// confiável; alto, o histórico está somando coisas diferentes.
	//
	// Zero quando não dá para calcular (sem dias, ou mediana dos mínimos
	// zerada).
	Dispersao float64
}

func computePeriodStats(days []gnjoy.PriceDayStat) periodStats {
	var stats periodStats
	if len(days) == 0 {
		return stats
	}
	preencherMedianas(&stats, days)

	stats.Days = len(days)
	stats.Min = days[0].MinItemPrice
	stats.Max = days[0].MaxItemPrice

	var totalQty int64
	var weightedSum float64
	for _, d := range days {
		if d.MinItemPrice < stats.Min {
			stats.Min = d.MinItemPrice
		}
		if d.MaxItemPrice > stats.Max {
			stats.Max = d.MaxItemPrice
		}
		totalQty += int64(d.ItemCnt)
		weightedSum += float64(d.AvgItemPrice) * float64(d.ItemCnt)
	}
	stats.QtySold = int(totalQty)
	if totalQty == 0 {
		return stats
	}
	stats.WeightedAvg = weightedSum / float64(totalQty)

	var weightedVarianceSum float64
	for _, d := range days {
		diff := float64(d.AvgItemPrice) - stats.WeightedAvg
		weightedVarianceSum += float64(d.ItemCnt) * diff * diff
	}
	stats.StdDev = math.Sqrt(weightedVarianceSum / float64(totalQty))

	return stats
}

// preencherMedianas calcula MedianMin, MedianMax e Dispersao. Separado do
// laço principal porque precisa das séries ordenadas, e ordenar exige cópia:
// a fatia que chega aqui é do cache compartilhado.
func preencherMedianas(stats *periodStats, days []gnjoy.PriceDayStat) {
	if len(days) == 0 {
		return
	}
	minimos := make([]int64, 0, len(days))
	maximos := make([]int64, 0, len(days))
	for _, d := range days {
		minimos = append(minimos, d.MinItemPrice)
		maximos = append(maximos, d.MaxItemPrice)
	}
	stats.MedianMin = mediana(minimos)
	stats.MedianMax = mediana(maximos)
	if stats.MedianMin > 0 {
		stats.Dispersao = float64(stats.MedianMax) / float64(stats.MedianMin)
	}
}

// mediana ordena uma cópia e devolve o valor do meio. Com um número par de
// elementos, a média dos dois centrais.
func mediana(valores []int64) int64 {
	if len(valores) == 0 {
		return 0
	}
	ordenados := slices.Clone(valores)
	slices.Sort(ordenados)
	meio := len(ordenados) / 2
	if len(ordenados)%2 == 1 {
		return ordenados[meio]
	}
	return (ordenados[meio-1] + ordenados[meio]) / 2
}
