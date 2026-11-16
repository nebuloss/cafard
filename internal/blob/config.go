package blob

import "errors"

// Config is the `images:` config section.
type Config struct {
	MaxFileMiB    int `yaml:"max_file_mib"`
	MaxFiles      int `yaml:"max_files"`
	MaxMegapixels int `yaml:"max_megapixels"`
}

// SetDefaults implements config.Defaulter.
func (c *Config) SetDefaults() {
	c.MaxFileMiB = 20
	c.MaxFiles = 10
	c.MaxMegapixels = 50
}

// Validate implements config.Validator.
func (c *Config) Validate() error {
	if c.MaxFileMiB < 1 || c.MaxFileMiB > 1024 {
		return errors.New("max_file_mib must be between 1 and 1024")
	}
	if c.MaxFiles < 1 || c.MaxFiles > 100 {
		return errors.New("max_files must be between 1 and 100")
	}
	if c.MaxMegapixels < 1 || c.MaxMegapixels > 1000 {
		return errors.New("max_megapixels must be between 1 and 1000")
	}
	return nil
}

// MaxFileBytes is MaxFileMiB in bytes.
func (c Config) MaxFileBytes() int64 { return int64(c.MaxFileMiB) << 20 }
