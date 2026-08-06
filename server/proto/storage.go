package proto

type GetImagesRsp struct {
	Files []string `json:"files"`
}

type MountImageReq struct {
	File     string `json:"file" form:"file" validate:"omitempty"`
	Cdrom    bool   `json:"cdrom" form:"cdrom" validate:"omitempty"`
	ReadOnly bool   `json:"readOnly" form:"readOnly" validate:"omitempty"`
	Source   string `json:"source" form:"source" validate:"omitempty"`
	Label    string `json:"label" form:"label" validate:"omitempty"`
}

type GetMountedImageRsp struct {
	File     string `json:"file"`
	Cdrom    bool   `json:"cdrom"`
	ReadOnly bool   `json:"readOnly"`
}

type DeleteImageReq struct {
	File string `json:"file" validate:"required"`
}

type ImageEnabledRsp struct {
	Enabled bool `json:"enabled"`
}

type StatusImageRsp struct {
	Status     string `json:"status"`
	File       string `json:"file"`
	Percentage string `json:"percentage"`
}
