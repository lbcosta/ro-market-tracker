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

// ShopSearchResult é o resultado da busca de lojas por nome de item.
type ShopSearchResult struct {
	Items      []ShopListItem `json:"list"`
	TotalCount int            `json:"totalCount"`
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
