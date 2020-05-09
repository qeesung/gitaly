package metadatahandler

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/labkit/correlation"
	"google.golang.org/grpc/metadata"
)

func TestAddMetadataTags(t *testing.T) {
	baseContext, cancel := testhelper.Context()
	defer cancel()

	testCases := []struct {
		desc             string
		metadata         metadata.MD
		deadline         bool
		expectedMetatags metadataTags
	}{
		{
			desc:     "empty metadata",
			metadata: metadata.Pairs(),
			deadline: false,
			expectedMetatags: metadataTags{
				clientName:   unknownValue,
				callSite:     unknownValue,
				authVersion:  unknownValue,
				deadlineType: "none",
			},
		},
		{
			desc:     "context containing metadata",
			metadata: metadata.Pairs("call_site", "testsite"),
			deadline: false,
			expectedMetatags: metadataTags{
				clientName:   unknownValue,
				callSite:     "testsite",
				authVersion:  unknownValue,
				deadlineType: "none",
			},
		},
		{
			desc:     "context containing metadata and a deadline",
			metadata: metadata.Pairs("call_site", "testsite"),
			deadline: true,
			expectedMetatags: metadataTags{
				clientName:   unknownValue,
				callSite:     "testsite",
				authVersion:  unknownValue,
				deadlineType: unknownValue,
			},
		},
		{
			desc:     "context containing metadata and a deadline type",
			metadata: metadata.Pairs("deadline_type", "regular"),
			deadline: true,
			expectedMetatags: metadataTags{
				clientName:   unknownValue,
				callSite:     unknownValue,
				authVersion:  unknownValue,
				deadlineType: "regular",
			},
		},
		{
			desc:     "a context without deadline but with deadline type",
			metadata: metadata.Pairs("deadline_type", "regular"),
			deadline: false,
			expectedMetatags: metadataTags{
				clientName:   unknownValue,
				callSite:     unknownValue,
				authVersion:  unknownValue,
				deadlineType: "none",
			},
		},
		{
			desc:     "with a context containing metadata",
			metadata: metadata.Pairs("deadline_type", "regular", "client_name", "rails"),
			deadline: true,
			expectedMetatags: metadataTags{
				clientName:   "rails",
				callSite:     unknownValue,
				authVersion:  unknownValue,
				deadlineType: "regular",
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.desc, func(t *testing.T) {
			ctx := metadata.NewIncomingContext(baseContext, testCase.metadata)
			if testCase.deadline {
				ctx, cancel = context.WithDeadline(ctx, time.Now().Add(50*time.Millisecond))
				defer cancel()
			}
			require.Equal(t, testCase.expectedMetatags, addMetadataTags(ctx))
		})
	}
}

func TestExtractClientNameFromContext(t *testing.T) {
	require := require.New(t)

	clientName := "CLIENT_NAME"

	ctx := metadata.NewIncomingContext(
		correlation.ContextWithClientName(
			context.Background(),
			clientName,
		),
		metadata.Pairs(),
	)

	metaTags := addMetadataTags(ctx)
	require.Equal(clientName, metaTags.clientName)
}
