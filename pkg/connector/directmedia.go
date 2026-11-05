// mautrix-slack - A Matrix-Slack puppeting bridge.
// Copyright (C) 2026 Tulir Asokan
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package connector

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/slack-go/slack"

	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/mediaproxy"

	"go.mau.fi/mautrix-slack/pkg/slackid"
)

type MediaIDType byte

const (
	MediaIDTypeEmoji MediaIDType = 1
)

func (s *SlackConnector) Download(ctx context.Context, mediaID networkid.MediaID, _ map[string]string) (mediaproxy.GetMediaResponse, error) {
	rawParsedID, err := ParseMediaID(mediaID)
	if err != nil {
		return nil, fmt.Errorf("failed to parse media ID: %w", err)
	}
	switch parsedID := rawParsedID.(type) {
	case *DirectMediaEmoji:
		return &mediaproxy.GetMediaResponseURL{
			URL: parsedID.URL(),
		}, nil
	case *DirectMediaFile:
		return s.downloadFile(ctx, parsedID)
	default:
		return nil, fmt.Errorf("unknown media ID type: %T", parsedID)
	}
}

func ParseMediaID(mediaID networkid.MediaID) (any, error) {
	if len(mediaID) == 0 {
		return nil, fmt.Errorf("empty media ID")
	}
	mediaIDType := MediaIDType(mediaID[0])
	mediaID = mediaID[1:]
	switch mediaIDType {
	case MediaIDTypeEmoji:
		teamID, mediaID, err := readShortString(mediaID)
		if err != nil {
			return nil, fmt.Errorf("failed to read team ID: %w", err)
		}
		emojiID, mediaID, err := readShortString(mediaID)
		if err != nil {
			return nil, fmt.Errorf("failed to read emoji ID: %w", err)
		}
		fileName, _, err := readShortString(mediaID)
		if err != nil {
			return nil, fmt.Errorf("failed to read file name: %w", err)
		}
		return &DirectMediaEmoji{
			TeamID:   teamID,
			EmojiID:  emojiID,
			FileName: fileName,
		}, nil
	case slackid.MediaIDTypeFile:
		teamID, mediaID, err := readShortString(mediaID)
		if err != nil {
			return nil, fmt.Errorf("failed to read team ID: %w", err)
		}
		fileID, _, err := readShortString(mediaID)
		if err != nil {
			return nil, fmt.Errorf("failed to read file ID: %w", err)
		}
		return &DirectMediaFile{TeamID: teamID, FileID: fileID}, nil
	default:
		return nil, fmt.Errorf("unknown media ID type: %d", mediaIDType)
	}
}

const slackEmojiURLPrefix = "https://emoji.slack-edge.com/"

type DirectMediaEmoji struct {
	TeamID   string
	EmojiID  string
	FileName string
}

func DirectMediaEmojiFromURL(url string) *DirectMediaEmoji {
	data, ok := strings.CutPrefix(url, slackEmojiURLPrefix)
	if !ok {
		return nil
	}
	parts := strings.Split(data, "/")
	if len(parts) != 3 {
		return nil
	}
	teamID := parts[0]
	emojiID := parts[1]
	fileName := parts[2]
	if len(teamID) > 16 || len(emojiID) > 100 || len(fileName) > 32 {
		return nil
	}
	return &DirectMediaEmoji{
		TeamID:   teamID,
		EmojiID:  emojiID,
		FileName: fileName,
	}
}

func (dme *DirectMediaEmoji) URL() string {
	if dme == nil {
		return ""
	}
	return fmt.Sprintf("%s%s/%s/%s", slackEmojiURLPrefix, dme.TeamID, dme.EmojiID, dme.FileName)
}

func (dme *DirectMediaEmoji) MediaID() networkid.MediaID {
	if dme == nil || len(dme.TeamID) > 16 || len(dme.EmojiID) > 100 || len(dme.FileName) > 32 {
		return nil
	}
	return slices.Concat(
		[]byte{byte(MediaIDTypeEmoji)},
		[]byte{byte(len(dme.TeamID))},
		[]byte(dme.TeamID),
		[]byte{byte(len(dme.EmojiID))},
		[]byte(dme.EmojiID),
		[]byte{byte(len(dme.FileName))},
		[]byte(dme.FileName),
	)
}

func readShortString(input []byte) (value string, remaining []byte, err error) {
	if len(input) == 0 {
		return "", nil, fmt.Errorf("input is empty")
	}
	length := int(input[0])
	if len(input) < 1+length {
		return "", nil, fmt.Errorf("input is too short for length %d", length)
	}
	value = string(input[1 : 1+length])
	remaining = input[1+length:]
	return
}

// DirectMediaFile is a file still on Slack, downloaded with the user's login when it's opened.
type DirectMediaFile struct {
	TeamID string
	FileID string
}

func (s *SlackConnector) downloadFile(ctx context.Context, dmf *DirectMediaFile) (mediaproxy.GetMediaResponse, error) {
	client := s.clientForTeam(dmf.TeamID)
	if client == nil {
		return nil, fmt.Errorf("not logged in to Slack team %s", dmf.TeamID)
	}
	// Download URLs can change, so ask Slack for the current one.
	file, _, _, err := client.GetFileInfoContext(ctx, dmf.FileID, 0, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to get file info: %w", err)
	}
	url := file.URLPrivateDownload
	if url == "" {
		url = file.URLPrivate
	}
	if url == "" {
		return nil, fmt.Errorf("file %s has no download URL", dmf.FileID)
	}
	return &mediaproxy.GetMediaResponseCallback{
		Callback: func(w io.Writer) (int64, error) {
			cw := &htmlCheckingWriter{w: w}
			err := client.GetFileContext(ctx, url, cw)
			return cw.n, err
		},
		ContentType:   file.Mimetype,
		ContentLength: int64(file.Size),
	}, nil
}

// clientForTeam finds a logged-in Slack client for the team a file belongs to.
func (s *SlackConnector) clientForTeam(teamID string) *slack.Client {
	for _, login := range s.br.GetAllCachedUserLogins() {
		if loginTeam, _ := slackid.ParseUserLoginID(login.ID); loginTeam != teamID {
			continue
		}
		if sc, ok := login.Client.(*SlackClient); ok && sc.Client != nil {
			return sc.Client
		}
	}
	return nil
}

var errHTMLDownload = errors.New("slack sent an HTML page instead of the file (expired login?)")

// htmlCheckingWriter counts bytes and refuses an HTML page, which Slack sends instead of the file when auth fails.
type htmlCheckingWriter struct {
	w io.Writer
	n int64
}

func (hw *htmlCheckingWriter) Write(p []byte) (int, error) {
	if hw.n == 0 && bytes.HasPrefix(bytes.TrimSpace(bytes.ToLower(p)), []byte("<!doctype html")) {
		return 0, errHTMLDownload
	}
	n, err := hw.w.Write(p)
	hw.n += int64(n)
	return n, err
}
