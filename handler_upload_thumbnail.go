package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadThumbnail(w http.ResponseWriter, r *http.Request) {
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	fmt.Println("uploading thumbnail for video", videoID, "by user", userID)

	const maxMemory = 10 << 20
	if err := r.ParseMultipartForm(maxMemory); err != nil {
		respondWithError(w, http.StatusBadRequest, "File too large", err)
		return
	}
	file, fileHeader, err := r.FormFile("thumbnail")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Incorrect header", err)
		return
	}
	mediaType := fileHeader.Header.Get("Content-Type")
	// imgByte, err := io.ReadAll(file)
	// if err != nil {
	// 	respondWithError(w, http.StatusInternalServerError, "Unable to read file", err)
	// 	return
	// }
	defer file.Close()

	metaData, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Video cannot be found", err)
		return
	}
	if metaData.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "You are not the owner", err)
		return
	}

	concatStr:= fmt.Sprintf("%v.%v", videoID, strings.Split(mediaType, "/")[1])
	trueFilePath := filepath.Join(cfg.assetsRoot, concatStr)
    fmt.Println(trueFilePath)

	diskFile, err := os.Create(trueFilePath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to save image through os create", err)
		return
	}
	defer diskFile.Close()
	if _, err := io.Copy(diskFile, file); err != nil {
		respondWithError(w, http.StatusInternalServerError, "unable to save image through io Copy", err)
		return
	}
	url := fmt.Sprintf("http://localhost:%v/assets/%v", cfg.port, concatStr)
	metaData.ThumbnailURL = &url
	if err := cfg.db.UpdateVideo(metaData); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to save video", err)
		return
	}

	respondWithJSON(w, http.StatusOK, metaData)
}
