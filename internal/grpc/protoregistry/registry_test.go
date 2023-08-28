package protoregistry

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewProtoRegistry(t *testing.T) {
	t.Parallel()

	expectedResults := map[string]map[string]OpType{
		"BlobService": {
			"GetBlob":        OpAccessor,
			"GetBlobs":       OpAccessor,
			"GetLFSPointers": OpAccessor,
		},
		"CleanupService": {
			"ApplyBfgObjectMapStream": OpMutator,
		},
		"CommitService": {
			"CommitIsAncestor":         OpAccessor,
			"CommitLanguages":          OpAccessor,
			"CommitStats":              OpAccessor,
			"CommitsByMessage":         OpAccessor,
			"CountCommits":             OpAccessor,
			"CountDivergingCommits":    OpAccessor,
			"FilterShasWithSignatures": OpAccessor,
			"FindAllCommits":           OpAccessor,
			"FindCommit":               OpAccessor,
			"FindCommits":              OpAccessor,
			"GetTreeEntries":           OpAccessor,
			"LastCommitForPath":        OpAccessor,
			"ListCommitsByOid":         OpAccessor,
			"ListFiles":                OpAccessor,
			"ListLastCommitsForTree":   OpAccessor,
			"RawBlame":                 OpAccessor,
			"TreeEntry":                OpAccessor,
		},
		"ConflictsService": {
			"ListConflictFiles": OpAccessor,
			"ResolveConflicts":  OpMutator,
		},
		"DiffService": {
			"CommitDelta": OpAccessor,
			"CommitDiff":  OpAccessor,
			"DiffStats":   OpAccessor,
			"RawDiff":     OpAccessor,
			"RawPatch":    OpAccessor,
		},
		"ObjectPoolService": {
			"CreateObjectPool":           OpMutator,
			"DeleteObjectPool":           OpMutator,
			"DisconnectGitAlternates":    OpMutator,
			"LinkRepositoryToObjectPool": OpMutator,
		},
		"OperationService": {
			"UserApplyPatch":      OpMutator,
			"UserCherryPick":      OpMutator,
			"UserCommitFiles":     OpMutator,
			"UserCreateBranch":    OpMutator,
			"UserCreateTag":       OpMutator,
			"UserDeleteBranch":    OpMutator,
			"UserDeleteTag":       OpMutator,
			"UserFFBranch":        OpMutator,
			"UserMergeBranch":     OpMutator,
			"UserMergeToRef":      OpMutator,
			"UserRevert":          OpMutator,
			"UserSquash":          OpMutator,
			"UserUpdateBranch":    OpMutator,
			"UserUpdateSubmodule": OpMutator,
		},
		"RefService": {
			"DeleteRefs":                      OpMutator,
			"FindAllBranches":                 OpAccessor,
			"FindAllRemoteBranches":           OpAccessor,
			"FindAllTags":                     OpAccessor,
			"FindBranch":                      OpAccessor,
			"FindDefaultBranchName":           OpAccessor,
			"FindLocalBranches":               OpAccessor,
			"GetTagMessages":                  OpAccessor,
			"ListBranchNamesContainingCommit": OpAccessor,
			"ListTagNamesContainingCommit":    OpAccessor,
			"RefExists":                       OpAccessor,
		},
		"RemoteService": {
			"FindRemoteRepository": OpAccessor,
			"FindRemoteRootRef":    OpAccessor,
			"UpdateRemoteMirror":   OpAccessor,
		},
		"RepositoryService": {
			"ApplyGitattributes":           OpMutator,
			"BackupCustomHooks":            OpAccessor,
			"CalculateChecksum":            OpAccessor,
			"CreateBundle":                 OpAccessor,
			"CreateFork":                   OpMutator,
			"CreateRepository":             OpMutator,
			"CreateRepositoryFromBundle":   OpMutator,
			"CreateRepositoryFromSnapshot": OpMutator,
			"CreateRepositoryFromURL":      OpMutator,
			"FetchBundle":                  OpMutator,
			"FetchRemote":                  OpMutator,
			"FetchSourceBranch":            OpMutator,
			"FindLicense":                  OpAccessor,
			"FindMergeBase":                OpAccessor,
			"Fsck":                         OpAccessor,
			"GetArchive":                   OpAccessor,
			"GetInfoAttributes":            OpAccessor,
			"GetRawChanges":                OpAccessor,
			"GetSnapshot":                  OpAccessor,
			"HasLocalBranches":             OpAccessor,
			"OptimizeRepository":           OpMaintenance,
			"PruneUnreachableObjects":      OpMaintenance,
			"RepositoryExists":             OpAccessor,
			"RepositorySize":               OpAccessor,
			"RestoreCustomHooks":           OpMutator,
			"SearchFilesByContent":         OpAccessor,
			"SearchFilesByName":            OpAccessor,
			"WriteRef":                     OpMutator,
		},
		"SmartHTTPService": {
			"InfoRefsReceivePack":           OpAccessor,
			"InfoRefsUploadPack":            OpAccessor,
			"PostReceivePack":               OpMutator,
			"PostUploadPackWithSidechannel": OpAccessor,
		},
		"SSHService": {
			"SSHReceivePack":   OpMutator,
			"SSHUploadArchive": OpAccessor,
			"SSHUploadPack":    OpAccessor,
		},
	}

	for serviceName, methods := range expectedResults {
		for methodName, opType := range methods {
			method := fmt.Sprintf("/gitaly.%s/%s", serviceName, methodName)

			methodInfo, err := GitalyProtoPreregistered.LookupMethod(method)
			require.NoError(t, err)

			require.Equalf(t, opType, methodInfo.Operation, "expect %s:%s to have the correct op type", serviceName, methodName)
			require.Equal(t, method, methodInfo.FullMethodName())
			require.False(t, GitalyProtoPreregistered.IsInterceptedMethod(method), method)
		}
	}
}

func TestNewProtoRegistry_IsInterceptedMethod(t *testing.T) {
	t.Parallel()

	for service, methods := range map[string][]string{
		"ServerService": {
			"ServerInfo",
			"DiskStatistics",
		},
		"PraefectInfoService": {
			"RepositoryReplicas",
			"DatalossCheck",
			"SetAuthoritativeStorage",
		},
		"RefTransaction": {
			"VoteTransaction",
			"StopTransaction",
		},
	} {
		t.Run(service, func(t *testing.T) {
			for _, method := range methods {
				t.Run(method, func(t *testing.T) {
					fullMethodName := fmt.Sprintf("/gitaly.%s/%s", service, method)
					require.True(t, GitalyProtoPreregistered.IsInterceptedMethod(fullMethodName))
					methodInfo, err := GitalyProtoPreregistered.LookupMethod(fullMethodName)
					require.Empty(t, methodInfo)
					require.Error(t, err, "full method name not found:")
				})
			}
		})
	}
}
