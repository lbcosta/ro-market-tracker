// Package gnjoy implementa um cliente para as rotas internas (não
// documentadas oficialmente) usadas pela página de busca de mercado do
// Ragnarok Online LATAM (https://ro.gnjoylatam.com).
//
// O site é construído em Next.js e as rotas consultadas pelo navegador não
// respondem JSON puro: usam o formato "React Server Components Flight",
// identificado pelo content-type "text/x-component". Esse formato é uma
// sequência de linhas "<id>:<valor>" onde a maior parte dos valores é JSON
// válido (o que este cliente explora para extrair os dados), intercalada com
// referências de módulo/erro que não são JSON e são simplesmente ignoradas.
package gnjoy

import (
	"encoding/json"
	"strings"
)

// StoreType é o tipo de negociação de uma loja de comércio.
type StoreType string

const (
	StoreTypeBuy  StoreType = "BUY"
	StoreTypeSell StoreType = "SELL"
)

// ShopListItem é um item retornado pela busca de lojas por nome de item.
type ShopListItem struct {
	SvrId              int    `json:"svrId"`
	ItemId             int    `json:"itemId"`
	MapId              int    `json:"mapId"`
	SSI                string `json:"ssi"`
	ItemName           string `json:"itemName"`
	DatabaseImgPath    string `json:"databaseImgPath"`
	DatabaseType       string `json:"databaseType"`
	StoreName          string `json:"storeName"`
	ItemPrice          int64  `json:"itemPrice"`
	ItemCnt            int    `json:"itemCnt"`
	SlotMaxCount       string `json:"slotMaxCount"`
	StoreTypeName      string `json:"storeTypeName"`
	ItemSellerCharName string `json:"itemSellerCharName"`
}

// DisplayName é o nome do item como ele aparece para o jogador: o nome de
// catálogo seguido do sufixo de slots, quando o item tem cartas.
//
// O site devolve as duas partes separadas — ItemName vem sem os slots e
// SlotMaxCount vem já entre colchetes ("[1]") ou vazio —, e itens que só
// diferem nisso são itens de catálogo DIFERENTES, com ItemId próprio e preço
// próprio: "Selo de Loki" (410232) e "Selo de Loki [1]" (410233). Mostrar só
// ItemName faz os dois aparecerem como o mesmo nome repetido, sem nada que
// explique a diferença de preço.
func (i ShopListItem) DisplayName() string {
	if i.SlotMaxCount == "" {
		return i.ItemName
	}
	return i.ItemName + " " + i.SlotMaxCount
}

// ShopSearchResult é o resultado da busca de lojas por nome de item.
type ShopSearchResult struct {
	Items      []ShopListItem `json:"list"`
	TotalCount int            `json:"totalCount"`
}

// MarketPriceItem é um item devolvido pela busca de preços de mercado: um
// resumo de por quanto ele andou sendo vendido no servidor, já agregado pelo
// próprio site no período pedido.
//
// SvrId, MapId e SSI vêm junto porque a lista é montada a partir de um
// anúncio de referência, mas o resumo (mínimo, médio, máximo e volume) é do
// item no servidor inteiro, não daquele anúncio.
type MarketPriceItem struct {
	SvrId           int    `json:"svrId"`
	ItemId          int    `json:"itemId"`
	MapId           int    `json:"mapId"`
	SSI             string `json:"ssi"`
	ItemName        string `json:"itemName"`
	DatabaseImgPath string `json:"databaseImgPath"`
	DatabaseType    string `json:"databaseType"`

	// TotalItemCnt é o volume negociado no período (a coluna "Vol" do site).
	TotalItemCnt int   `json:"totalItemCnt"`
	MinItemPrice int64 `json:"minItemPrice"`
	MaxItemPrice int64 `json:"maxItemPrice"`
	AvgItemPrice int64 `json:"avgItemPrice"`
}

// MarketPriceResult é o resultado da busca de preços de mercado por nome de
// item. Um termo casa por trecho do nome, então a lista costuma ter mais de
// um item ("rapidez" traz "Módulo de S-Rapidez" e "Automódulo de M-Rapidez").
type MarketPriceResult struct {
	Items      []MarketPriceItem `json:"list"`
	TotalCount int               `json:"totalCount"`
}

// StoreDetail são os detalhes de uma loja específica (identificada por
// svrId+mapId+ssi, o "endereço" de uma loja aberta por um personagem no
// mundo do jogo).
type StoreDetail struct {
	SvrId               int    `json:"svrId"`
	SvrName             string `json:"svrName"`
	ItemId              int    `json:"itemId"`
	MapId               int    `json:"mapId"`
	SSI                 string `json:"ssi"`
	StoreName           string `json:"storeName"`
	MapName             string `json:"mapName"`
	ItemSellerCharName  string `json:"itemSellerCharName"`
	ItemFullName        string `json:"itemFullName"`
	ItemPrice           int64  `json:"itemPrice"`
	MarketStoreTypeCode string `json:"marketStoreTypeCode"`
	ItemCnt             int    `json:"itemCnt"`
	DatabaseImgPath     string `json:"databaseImgPath"`
	DatabaseType        string `json:"databaseType"`
	Xpos                string `json:"xpos"`
	Ypos                string `json:"ypos"`

	// Refine é o nível de refino do equipamento (ex.: 7 para "+7"), extraído
	// do prefixo "+N" em ItemFullName. É 0 tanto para um item sem refino
	// quanto para um item que não é equipamento refinável (armas/armaduras
	// não têm esse prefixo) — não há como distinguir os dois casos a partir
	// dos dados que o site expõe.
	Refine int `json:"refine"`
}

// UnmarshalJSON decodifica StoreDetail normalmente e, em seguida, calcula
// Refine a partir de ItemFullName — o site não expõe o refino como um campo
// próprio, só embutido nesse prefixo do nome completo do item.
func (d *StoreDetail) UnmarshalJSON(data []byte) error {
	type alias StoreDetail
	if err := json.Unmarshal(data, (*alias)(d)); err != nil {
		return err
	}
	d.Refine = parseRefine(d.ItemFullName)
	return nil
}

// ItemDetail são os detalhes do item sendo vendido/comprado em uma loja
// específica.
type ItemDetail struct {
	SvrId              int     `json:"svrId"`
	ItemId             int     `json:"itemId"`
	ItemName           string  `json:"itemName"`
	ItemPrice          int64   `json:"itemPrice"`
	MapId              int     `json:"mapId"`
	SSI                string  `json:"ssi"`
	ItemType           string  `json:"itemType"`
	ItemOptionProperty *string `json:"itemOptionProperty"`
	RandomOpt1         *string `json:"randomOpt1"`
	RandomOpt2         *string `json:"randomOpt2"`
	RandomOpt3         *string `json:"randomOpt3"`
	RandomOpt4         *string `json:"randomOpt4"`
	Slot1              *string `json:"slot1"`
	Slot2              *string `json:"slot2"`
	Slot3              *string `json:"slot3"`
	Slot4              *string `json:"slot4"`
	HasDatabaseItem    bool    `json:"hasDatabaseItem"`
	DatabaseImgPath    string  `json:"databaseImgPath"`
	DatabaseType       string  `json:"databaseType"`
}

// RandomOptions são os bônus aleatórios ("BA") da unidade anunciada, na ordem
// dos quatro campos que o site expõe e já sem os vazios. Cada um vem como uma
// frase pronta no idioma pedido em GetItemDetail ("CRIT +4", "Conjuração
// variável -5%").
//
// São uma propriedade do ANÚNCIO, não do item de catálogo: duas unidades do
// mesmo item têm bônus diferentes, e é por eles que o preço de um equipamento
// varia mais do que por qualquer outra coisa. Como o refino, a única rota que
// os expõe é o detalhe — a busca por nome não os traz —, então descobri-los
// custa uma requisição por anúncio (ver internal/web/bonus.go).
func (d *ItemDetail) RandomOptions() []string {
	opts := make([]string, 0, 4)
	for _, opt := range []*string{d.RandomOpt1, d.RandomOpt2, d.RandomOpt3, d.RandomOpt4} {
		if opt == nil {
			continue
		}
		if trimmed := strings.TrimSpace(*opt); trimmed != "" {
			opts = append(opts, trimmed)
		}
	}
	return opts
}

// PriceChartPoint é um ponto do histórico de preço (série resumida, usada
// para gráficos) de um item em um dia específico.
type PriceChartPoint struct {
	Date         string `json:"nowDate"`
	MinItemPrice int64  `json:"minItemPrice"`
	MaxItemPrice int64  `json:"maxItemPrice"`
	AvgItemPrice int64  `json:"avgItemPrice"`
}

// PriceDayStat é um ponto do histórico de preço com estatísticas adicionais
// de volume de anúncios naquele dia.
type PriceDayStat struct {
	Date         string `json:"nowDate"`
	MinItemPrice int64  `json:"minItemPrice"`
	MaxItemPrice int64  `json:"maxItemPrice"`
	AvgItemPrice int64  `json:"avgItemPrice"`
	ItemCnt      int    `json:"itemCnt"`
	TotalCount   int    `json:"totalCount"`
}

// PriceHistory é o histórico de preços pelo qual um item já foi
// anunciado/vendido no mercado.
type PriceHistory struct {
	ItemPriceMin int64             `json:"itemPriceMin"`
	ItemPriceMax int64             `json:"itemPriceMax"`
	ChartList    []PriceChartPoint `json:"priceDetailChartList"`
	DayStatsList []PriceDayStat    `json:"priceDetailDayList"`
}
