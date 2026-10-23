package backend

import "os"

func osStatPath(path string) (os.FileInfo, error) {
	return os.Stat(path)
}

func osIsNotExist(err error) bool {
	return err != nil && os.IsNotExist(err)
}
