package web

import (
	"math"

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
}

func computePeriodStats(days []gnjoy.PriceDayStat) periodStats {
	var stats periodStats
	if len(days) == 0 {
		return stats
	}

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
