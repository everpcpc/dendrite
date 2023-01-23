// Copyright 2017 Vector Creations Ltd
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package routing

import (
	"context"
	"net/http"

	"github.com/matrix-org/dendrite/clientapi/auth"
	"github.com/matrix-org/dendrite/clientapi/auth/authtypes"
	"github.com/matrix-org/dendrite/clientapi/jsonerror"
	"github.com/matrix-org/dendrite/clientapi/userutil"
	"github.com/matrix-org/dendrite/setup/config"
	userapi "github.com/matrix-org/dendrite/userapi/api"
	"github.com/matrix-org/util"
)

type loginResponse struct {
	UserID      string `json:"user_id"`
	AccessToken string `json:"access_token"`
	DeviceID    string `json:"device_id"`
}

type flows struct {
	Flows []stage `json:"flows"`
}

type stage struct {
	Type              string             `json:"type"`
	IdentityProviders []identityProvider `json:"identity_providers,omitempty"`
}

type identityProvider struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Brand config.SSOBrand `json:"brand,omitempty"`
	Icon  string          `json:"icon,omitempty"`
}

func passwordLogin() []stage {
	return []stage{
		{Type: authtypes.LoginTypePassword},
	}
}

func ssoLogin(cfg *config.ClientAPI) []stage {
	if !cfg.Login.SSO.Enabled {
		return nil
	}

	var idps []identityProvider
	for _, idp := range cfg.Login.SSO.Providers {
		brand := idp.Brand
		if brand == "" {
			typ := idp.Type
			if typ == "" {
				typ = config.IdentityProviderType(idp.ID)
			}
			switch typ {
			case config.SSOTypeGitHub:
				brand = config.SSOBrandGitHub

			case config.SSOTypeMastodon:
				brand = config.SSOBrandMastodon

			default:
				brand = config.SSOBrand(idp.ID)
			}
		}
		idps = append(idps, identityProvider{
			ID:    idp.ID,
			Name:  idp.Name,
			Brand: brand,
			Icon:  idp.Icon,
		})
	}
	return []stage{
		{
			Type:              authtypes.LoginTypeSSO,
			IdentityProviders: idps,
		},
	}
}

func tokenLogin(cfg *config.ClientAPI) []stage {
	if !cfg.Login.LoginTokenEnabled() {
		return nil
	}

	return []stage{
		{
			Type: authtypes.LoginTypeToken,
		},
	}
}

// Login implements GET and POST /login
func Login(
	req *http.Request, userAPI userapi.ClientUserAPI,
	cfg *config.ClientAPI,
) util.JSONResponse {
	if req.Method == http.MethodGet {
		allFlows := passwordLogin()
		allFlows = append(allFlows, ssoLogin(cfg)...)
		allFlows = append(allFlows, tokenLogin(cfg)...)
		return util.JSONResponse{
			Code: http.StatusOK,
			JSON: flows{Flows: allFlows},
		}
	} else if req.Method == http.MethodPost {
		login, cleanup, authErr := auth.LoginFromJSONReader(req.Context(), req.Body, userAPI, userAPI, cfg)
		if authErr != nil {
			return *authErr
		}
		// make a device/access token
		authErr2 := completeAuth(req.Context(), cfg.Matrix, userAPI, login, req.RemoteAddr, req.UserAgent())
		cleanup(req.Context(), &authErr2)
		return authErr2
	}

	return util.JSONResponse{
		Code: http.StatusMethodNotAllowed,
		JSON: jsonerror.NotFound("Bad method"),
	}
}

func completeAuth(
	ctx context.Context, cfg *config.Global, userAPI userapi.ClientUserAPI, login *auth.Login,
	ipAddr, userAgent string,
) util.JSONResponse {
	token, err := auth.GenerateAccessToken()
	if err != nil {
		util.GetLogger(ctx).WithError(err).Error("auth.GenerateAccessToken failed")
		return jsonerror.InternalServerError()
	}

	localpart, serverName, err := userutil.ParseUsernameParam(login.Username(), cfg)
	if err != nil {
		util.GetLogger(ctx).WithError(err).Error("auth.ParseUsernameParam failed")
		return jsonerror.InternalServerError()
	}

	var performRes userapi.PerformDeviceCreationResponse
	err = userAPI.PerformDeviceCreation(ctx, &userapi.PerformDeviceCreationRequest{
		DeviceDisplayName: login.InitialDisplayName,
		DeviceID:          login.DeviceID,
		AccessToken:       token,
		Localpart:         localpart,
		ServerName:        serverName,
		IPAddr:            ipAddr,
		UserAgent:         userAgent,
	}, &performRes)
	if err != nil {
		return util.JSONResponse{
			Code: http.StatusInternalServerError,
			JSON: jsonerror.Unknown("failed to create device: " + err.Error()),
		}
	}

	return util.JSONResponse{
		Code: http.StatusOK,
		JSON: loginResponse{
			UserID:      performRes.Device.UserID,
			AccessToken: performRes.Device.AccessToken,
			DeviceID:    performRes.Device.ID,
		},
	}
}
