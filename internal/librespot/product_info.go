package librespot

import (
	"encoding/hex"
	"encoding/xml"
	"strings"
)

type ProductInfo struct {
	XMLName  xml.Name `xml:"products"`
	Products []struct {
		XMLName      xml.Name `xml:"product"`
		Type         string   `xml:"type"`
		HeadFilesUrl string   `xml:"head-files-url"`
		ImageUrl     string   `xml:"image-url"`
	} `xml:"product"`
}

func (pi ProductInfo) ImageUrl(fileID []byte) *string {
	if len(pi.Products) == 0 || pi.Products[0].ImageUrl == "" {
		return nil
	}
	if len(fileID) == 0 {
		return nil
	}
	fileIDHex := strings.ToLower(hex.EncodeToString(fileID))
	val := strings.Replace(pi.Products[0].ImageUrl, "{file_id}", fileIDHex, 1)
	return &val
}
