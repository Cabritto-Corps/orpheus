package librespot

type ApiResponseStatusTrack struct {
	Name          string   `json:"name"`
	ArtistNames   []string `json:"artist_names"`
	AlbumName     string   `json:"album_name"`
	AlbumCoverUrl *string  `json:"album_cover_url"`
	Position      int64    `json:"position"`
}
