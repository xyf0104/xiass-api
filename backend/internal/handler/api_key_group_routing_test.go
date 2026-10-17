package handler

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type apiKeyGroupRoutingCatalogStub struct {
	models map[int64][]string
	err    map[int64]error
}

func (s apiKeyGroupRoutingCatalogStub) ModelsForAPIKeyGroup(_ context.Context, group *service.Group) ([]string, error) {
	if group == nil {
		return nil, nil
	}
	if err := s.err[group.ID]; err != nil {
		return nil, err
	}
	return append([]string(nil), s.models[group.ID]...), nil
}

func newAPIKeyGroupRoutingContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/", nil)
	return c
}

func hydratedRoutingGroup(id int64, name string) *service.Group {
	return &service.Group{ID: id, Name: name, Platform: service.PlatformOpenAI, Status: service.StatusActive, Hydrated: true}
}

func TestSelectAPIKeyGroupForModelUsesExactModelCatalogMatch(t *testing.T) {
	chat := hydratedRoutingGroup(10, "chat")
	image := hydratedRoutingGroup(20, "image")
	primaryOverride := 7
	apiKey := &service.APIKey{
		Group:    chat,
		GroupID:  &chat.ID,
		Groups:   []*service.Group{chat, image},
		GroupIDs: []int64{chat.ID, image.ID},
		User:     &service.User{UserGroupRPMOverride: &primaryOverride},
	}
	c := newAPIKeyGroupRoutingContext()

	ok := selectAPIKeyGroupForModel(context.Background(), c, apiKey, "gpt-image-2", apiKeyGroupRoutingCatalogStub{
		models: map[int64][]string{chat.ID: {"gpt-5.6"}, image.ID: {"gpt-image-2"}},
	}, false)

	require.True(t, ok)
	require.Same(t, image, apiKey.Group)
	require.Equal(t, image.ID, *apiKey.GroupID)
	require.Nil(t, apiKey.User.UserGroupRPMOverride)
}

func TestSelectAPIKeyGroupForModelPrefersImageCapableGroupForExplicitImageIntent(t *testing.T) {
	chat := hydratedRoutingGroup(10, "chat")
	image := hydratedRoutingGroup(20, "image")
	image.AllowImageGeneration = true
	apiKey := &service.APIKey{Group: chat, GroupID: &chat.ID, Groups: []*service.Group{chat, image}, GroupIDs: []int64{chat.ID, image.ID}}
	c := newAPIKeyGroupRoutingContext()

	ok := selectAPIKeyGroupForModel(context.Background(), c, apiKey, "gpt-5.6", apiKeyGroupRoutingCatalogStub{
		models: map[int64][]string{chat.ID: {"gpt-5.6"}, image.ID: nil},
	}, true)

	require.True(t, ok)
	require.Same(t, chat, apiKey.Group)

	apiKey.Group = chat
	apiKey.GroupID = &chat.ID
	ok = selectAPIKeyGroupForModel(context.Background(), c, apiKey, "gpt-image-2", apiKeyGroupRoutingCatalogStub{
		models: map[int64][]string{chat.ID: nil, image.ID: nil},
	}, true)
	require.True(t, ok)
	require.Same(t, image, apiKey.Group)
}

func TestSelectAPIKeyGroupForModelUnknownModelKeepsPrimaryGroup(t *testing.T) {
	primary := hydratedRoutingGroup(10, "primary")
	secondary := hydratedRoutingGroup(20, "secondary")
	apiKey := &service.APIKey{Group: primary, GroupID: &primary.ID, Groups: []*service.Group{primary, secondary}, GroupIDs: []int64{primary.ID, secondary.ID}}
	c := newAPIKeyGroupRoutingContext()

	ok := selectAPIKeyGroupForModel(context.Background(), c, apiKey, "unknown-model", apiKeyGroupRoutingCatalogStub{
		models: map[int64][]string{primary.ID: {"gpt-5.6"}, secondary.ID: {"claude-opus"}},
	}, false)

	require.True(t, ok)
	require.Same(t, primary, apiKey.Group)
	require.Equal(t, primary.ID, *apiKey.GroupID)
}

func TestUnionAPIKeyGroupModelsDeduplicatesCaseInsensitiveIDs(t *testing.T) {
	first := hydratedRoutingGroup(10, "first")
	second := hydratedRoutingGroup(20, "second")
	models, groups, err := unionAPIKeyGroupModels(context.Background(), apiKeyGroupRoutingCatalogStub{
		models: map[int64][]string{first.ID: {"gpt-5.6", "GPT-Shared"}, second.ID: {"gpt-shared", "gpt-image-2"}},
	}, []*service.Group{first, second}, "", nil)

	require.NoError(t, err)
	require.Len(t, groups, 2)
	require.Equal(t, []string{"gpt-5.6", "GPT-Shared", "gpt-image-2"}, models)
}
