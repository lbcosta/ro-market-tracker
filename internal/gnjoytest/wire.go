// Package gnjoytest implementa um mock do site do GnJoy LATAM: um servidor
// HTTP que fala o mesmo protocolo das rotas internas consumidas por
// internal/gnjoy — busca no formato RSC Flight, Server Actions do Next.js e a
// descoberta do action id varrendo os chunks JS da página.
//
// Existe para que os testes (tanto os de Go quanto os de navegador) nunca
// toquem a API real: o site do GnJoy LATAM tem rate limiting próprio e não é
// uma API pública documentada, então bater nele a partir de uma suíte de
// testes é ao mesmo tempo frágil e um jeito rápido de tomar bloqueio.
//
// Os tipos deste arquivo são declarados de forma INDEPENDENTE dos tipos de
// internal/gnjoy de propósito: eles descrevem o formato que o site de fato
// devolve (capturado em docs/webtools-api-research.md), não o formato que o
// client espera receber. Se um json tag do client divergir do contrato real,
// os testes quebram — o que não aconteceria se mock e client compartilhassem
// a mesma struct e apenas fizessem round-trip um do outro.
package gnjoytest

// ShopListItem é um item da lista devolvida pela busca de lojas.
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

// SearchResult é o que a busca devolve para um termo: a lista de anúncios e
// o total. Se TotalCount for zero, o mock usa len(Items).
type SearchResult struct {
	Items      []ShopListItem
	TotalCount int
}

// MarketPriceItem é um item da lista devolvida pela busca de preços de
// mercado: o resumo de vendas do item no servidor, já agregado pelo site.
type MarketPriceItem struct {
	SvrId           int    `json:"svrId"`
	ItemId          int    `json:"itemId"`
	MapId           int    `json:"mapId"`
	SSI             string `json:"ssi"`
	ItemName        string `json:"itemName"`
	DatabaseImgPath string `json:"databaseImgPath"`
	DatabaseType    string `json:"databaseType"`
	TotalItemCnt    int    `json:"totalItemCnt"`
	MinItemPrice    int64  `json:"minItemPrice"`
	MaxItemPrice    int64  `json:"maxItemPrice"`
	AvgItemPrice    int64  `json:"avgItemPrice"`
}

// MarketPriceResult é o que a busca de preços de mercado devolve para um
// termo: um resumo por item que casou com ele, e o total.
type MarketPriceResult struct {
	Items      []MarketPriceItem
	TotalCount int
}

// StoreDetail são os detalhes de uma loja específica, como devolvidos pela
// Server Action "store".
//
// Repare que NÃO existe um campo "refine" aqui: o site só expõe o refino
// embutido no prefixo "+N" de ItemFullName, e é justamente isso que o client
// precisa saber interpretar.
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

// ItemDetail são os detalhes do item anunciado, como devolvidos pela Server
// Action "item".
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

// PricePoint é um dia da série resumida do histórico de preço.
type PricePoint struct {
	Date         string `json:"nowDate"`
	MinItemPrice int64  `json:"minItemPrice"`
	MaxItemPrice int64  `json:"maxItemPrice"`
	AvgItemPrice int64  `json:"avgItemPrice"`
}

// PriceDayStat é um dia do histórico com os agregados de volume.
type PriceDayStat struct {
	Date         string `json:"nowDate"`
	MinItemPrice int64  `json:"minItemPrice"`
	MaxItemPrice int64  `json:"maxItemPrice"`
	AvgItemPrice int64  `json:"avgItemPrice"`
	ItemCnt      int    `json:"itemCnt"`
	TotalCount   int    `json:"totalCount"`
}

// PriceHistory é o histórico devolvido pela Server Action "price".
type PriceHistory struct {
	ItemPriceMin int64          `json:"itemPriceMin"`
	ItemPriceMax int64          `json:"itemPriceMax"`
	ChartList    []PricePoint   `json:"priceDetailChartList"`
	DayStatsList []PriceDayStat `json:"priceDetailDayList"`
}

// ActionRequest é o corpo de uma chamada de Server Action, como o client o
// monta: uma lista de um elemento com o tipo e os parâmetros.
type ActionRequest struct {
	Type   string         `json:"type"`
	Params map[string]any `json:"params"`
}
