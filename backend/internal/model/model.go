package model

type Article struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Summary     string `json:"summary"`
	URL         string `json:"url"`
	SourceID    string `json:"source_id"`
	SourceName  string `json:"source_name"`
	Tag         string `json:"tag"`
	ImagePath   string `json:"image_path"`
	PublishedAt string `json:"published_at"`
	IsDemo      bool   `json:"is_demo"`
}
type Issue struct {
	Number string `json:"number"`
	Date   string `json:"date"`
	Title  string `json:"title"`
}
type Nav struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}
type Ad struct {
	ID         string `json:"id"`
	Placement  string `json:"placement"`
	Title      string `json:"title"`
	Advertiser string `json:"advertiser"`
	ERID       string `json:"erid"`
	ClickURL   string `json:"click_url"`
	File       string `json:"file"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
}
type SourceStatus struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	Status      string `json:"status"`
	LastSuccess string `json:"last_success"`
	LastError   string `json:"last_error,omitempty"`
	ItemCount   int    `json:"item_count"`
}
type Bundle struct {
	Issue      Issue          `json:"issue"`
	Nav        []Nav          `json:"nav"`
	Hero       *Article       `json:"hero"`
	Feed       []Article      `json:"feed"`
	Stories    []Article      `json:"stories"`
	Scoreboard []any          `json:"scoreboard"`
	Opinions   []any          `json:"opinions"`
	Ads        []Ad           `json:"ads"`
	UpdatedAt  string         `json:"updated_at"`
	IsDemo     bool           `json:"is_demo"`
	Sources    []SourceStatus `json:"sources"`
}

func Navigation() []Nav {
	return []Nav{{"all", "Все"}, {"football", "Футбол"}, {"hockey", "Хоккей"}, {"tennis", "Теннис"}, {"basketball", "Баскетбол"}, {"motorsport", "Автоспорт"}, {"other", "Вне игры"}}
}
