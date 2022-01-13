package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/grafana/grafana/pkg/api/dtos"
	"github.com/grafana/grafana/pkg/api/response"
	"github.com/grafana/grafana/pkg/bus"
	"github.com/grafana/grafana/pkg/models"
	"github.com/grafana/grafana/pkg/services/accesscontrol/resourcepermissions"
	"github.com/grafana/grafana/pkg/util"
	"github.com/grafana/grafana/pkg/web"
)

// GET /api/teams/:teamId/members
func (hs *HTTPServer) GetTeamMembers(c *models.ReqContext) response.Response {
	query := models.GetTeamMembersQuery{OrgId: c.OrgId, TeamId: c.ParamsInt64(":teamId")}

	if err := bus.Dispatch(c.Req.Context(), &query); err != nil {
		return response.Error(500, "Failed to get Team Members", err)
	}

	filteredMembers := make([]*models.TeamMemberDTO, 0, len(query.Result))
	for _, member := range query.Result {
		// TODO: when FGAC is enabled, use SQL filtering to filter out for users that the caller has permissions to see
		if !hs.Cfg.FeatureToggles["accesscontrol"] && dtos.IsHiddenUser(member.Login, c.SignedInUser, hs.Cfg) {
			continue
		}

		member.AvatarUrl = dtos.GetGravatarUrl(member.Email)
		member.Labels = []string{}

		if hs.License.FeatureEnabled("teamgroupsync") && member.External {
			authProvider := GetAuthProviderLabel(member.AuthModule)
			member.Labels = append(member.Labels, authProvider)
		}

		filteredMembers = append(filteredMembers, member)
	}

	return response.JSON(200, filteredMembers)
}

// POST /api/teams/:teamId/members
func (hs *HTTPServer) AddTeamMember(c *models.ReqContext) response.Response {
	cmd := models.AddTeamMemberCommand{}
	if err := web.Bind(c.Req, &cmd); err != nil {
		return response.Error(http.StatusBadRequest, "bad request data", err)
	}
	cmd.OrgId = c.OrgId
	cmd.TeamId = c.ParamsInt64(":teamId")

	if !hs.Cfg.FeatureToggles["accesscontrol"] {
		if err := hs.teamGuardian.CanAdmin(c.Req.Context(), cmd.OrgId, cmd.TeamId, c.SignedInUser); err != nil {
			return response.Error(403, "Not allowed to add team member", err)
		}
	}

	isTeamMember, err := hs.SQLStore.IsTeamMember(c.OrgId, cmd.TeamId, cmd.UserId)
	if err != nil {
		return response.Error(500, "Failed to update team member.", err)
	}
	if isTeamMember {
		return response.Error(400, "User is already added to this team", nil)
	}

	err = addOrUpdateTeamMember(hs.TeamPermissionsService, cmd.UserId, cmd.OrgId, cmd.TeamId, cmd.External, cmd.Permission)
	if err != nil {
		return response.Error(500, "Failed to add Member to Team", err)
	}

	return response.JSON(200, &util.DynMap{
		"message": "Member added to Team",
	})
}

// PUT /:teamId/members/:userId
func (hs *HTTPServer) UpdateTeamMember(c *models.ReqContext) response.Response {
	cmd := models.UpdateTeamMemberCommand{}
	if err := web.Bind(c.Req, &cmd); err != nil {
		return response.Error(http.StatusBadRequest, "bad request data", err)
	}
	teamId := c.ParamsInt64(":teamId")
	userId := c.ParamsInt64(":userId")
	orgId := c.OrgId

	if !hs.Cfg.FeatureToggles["accesscontrol"] {
		if err := hs.teamGuardian.CanAdmin(c.Req.Context(), orgId, teamId, c.SignedInUser); err != nil {
			return response.Error(403, "Not allowed to update team member", err)
		}
	}

	isTeamMember, err := hs.SQLStore.IsTeamMember(orgId, teamId, userId)
	if err != nil {
		return response.Error(500, "Failed to update team member.", err)
	}
	if !isTeamMember {
		return response.Error(404, "Team member not found.", nil)
	}

	err = addOrUpdateTeamMember(hs.TeamPermissionsService, userId, orgId, teamId, false, cmd.Permission)
	if err != nil {
		return response.Error(500, "Failed to update team member.", err)
	}
	return response.Success("Team member updated")
}

// DELETE /api/teams/:teamId/members/:userId
func (hs *HTTPServer) RemoveTeamMember(c *models.ReqContext) response.Response {
	orgId := c.OrgId
	teamId := c.ParamsInt64(":teamId")
	userId := c.ParamsInt64(":userId")

	if !hs.Cfg.FeatureToggles["accesscontrol"] {
		if err := hs.teamGuardian.CanAdmin(c.Req.Context(), orgId, teamId, c.SignedInUser); err != nil {
			return response.Error(403, "Not allowed to remove team member", err)
		}
	}

	teamIDString := strconv.FormatInt(teamId, 10)
	if _, err := hs.TeamPermissionsService.SetUserPermission(context.TODO(), orgId, userId, teamIDString, []string{}); err != nil {
		if errors.Is(err, models.ErrTeamNotFound) {
			return response.Error(404, "Team not found", nil)
		}

		if errors.Is(err, models.ErrTeamMemberNotFound) {
			return response.Error(404, "Team member not found", nil)
		}

		return response.Error(500, "Failed to remove Member from Team", err)
	}
	return response.Success("Team Member removed")
}

// addOrUpdateTeamMember adds or updates a team member.
//
// Stubbable by tests.
var addOrUpdateTeamMember = func(resourcePermissionService *resourcepermissions.Service, userID, orgID, teamID int64, isExternal bool,
	permission models.PermissionType) error {
	permissionString := permission.String()
	// Team member permission is 0, which maps to an empty string.
	// However, we want the team permission service to display "Member" for team members. This is a hack to make it work.
	if permissionString == "" {
		permissionString = "Member"
	}
	actions := resourcePermissionService.MapPermission(permissionString)
	teamIDString := strconv.FormatInt(teamID, 10)
	_, err := resourcePermissionService.SetUserPermission(context.TODO(), orgID, userID, teamIDString, actions)

	return err
}
