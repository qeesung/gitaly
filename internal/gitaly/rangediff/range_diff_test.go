package rangediff

import (
	"crypto/sha1"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func TestRangeDiffParserOneLineEqual(t *testing.T) {
	t.Parallel()
	rawRangeDiff := "1:  ed49d4bc090707090608de929fbf7d8497f35ec8 = 1:  ed49d4bc090707090608de929fbf7d8497f35ec8 hello10"

	commitPairs := getRangeDiffs(t, rawRangeDiff)
	expectedCommitPairs := []*CommitPair{
		{
			FromCommit:         "ed49d4bc090707090608de929fbf7d8497f35ec8",
			ToCommit:           "ed49d4bc090707090608de929fbf7d8497f35ec8",
			Comparison:         gitalypb.RangeDiffResponse_COMPARATOR_EQUAL_UNSPECIFIED,
			CommitMessageTitle: "hello10",
		},
	}

	require.Equal(t, expectedCommitPairs, commitPairs)
}

func TestRangeDiffParserOneLineWithNotEqual(t *testing.T) {
	t.Parallel()
	rawRangeDiff := `1:  52cefcc80f3cf1f092ab48cf3e9e8c3846080aef ! 1:  e9c39ac3537b9423b9db4585c3f5de72703fd75c test commit message title 2
    @@ Metadata
     Author: Scrooge McDuck <scrooge@mcduck.com>
    
      ## Commit message ##
    -    test commit message title 2
    +    test commit message title 1
    
      ## foo ##
     @@
`

	commitPairs := getRangeDiffs(t, rawRangeDiff)
	expectedCommitPairs := []*CommitPair{
		{
			FromCommit:         "52cefcc80f3cf1f092ab48cf3e9e8c3846080aef",
			ToCommit:           "e9c39ac3537b9423b9db4585c3f5de72703fd75c",
			Comparison:         gitalypb.RangeDiffResponse_COMPARATOR_NOT_EQUAL,
			CommitMessageTitle: "test commit message title 2",
			PatchData: []byte(`@@ Metadata
 Author: Scrooge McDuck <scrooge@mcduck.com>

  ## Commit message ##
-    test commit message title 2
+    test commit message title 1

  ## foo ##
 @@
`),
		},
	}

	require.Equal(t, expectedCommitPairs, commitPairs)
}

func TestRangeDiffParserWithNotEqual(t *testing.T) {
	t.Parallel()
	rawRangeDiff := `1:  ed49d4bc090707090608de929fbf7d8497f35ec8 = 1:  ed49d4bc090707090608de929fbf7d8497f35ec8 hello10
2:  8fc97446be021b44e534421265a138c5bb39a3dc ! 2:  73afbb7e27dac8cade2dd014271d53baab714f1f hello12
    @@ Metadata
     Author: ZheNing Hu <adlternative@gamil.com>
    
      ## Commit message ##
    -    hello12
    +    hello11
    
      ## 11 (new) ##
     @@
`

	commitPairs := getRangeDiffs(t, rawRangeDiff)
	expectedCommitPairs := []*CommitPair{
		{
			FromCommit:         "ed49d4bc090707090608de929fbf7d8497f35ec8",
			ToCommit:           "ed49d4bc090707090608de929fbf7d8497f35ec8",
			Comparison:         gitalypb.RangeDiffResponse_COMPARATOR_EQUAL_UNSPECIFIED,
			CommitMessageTitle: "hello10",
		},
		{
			FromCommit:         "8fc97446be021b44e534421265a138c5bb39a3dc",
			ToCommit:           "73afbb7e27dac8cade2dd014271d53baab714f1f",
			Comparison:         gitalypb.RangeDiffResponse_COMPARATOR_NOT_EQUAL,
			CommitMessageTitle: "hello12",
			PatchData: []byte(`@@ Metadata
 Author: ZheNing Hu <adlternative@gamil.com>

  ## Commit message ##
-    hello12
+    hello11

  ## 11 (new) ##
 @@
`),
		},
	}

	require.Equal(t, expectedCommitPairs, commitPairs)
}

func TestRangeDiffParserBasicWithFourComparison(t *testing.T) {
	t.Parallel()
	rawRangeDiff := `1:  ed49d4bc090707090608de929fbf7d8497f35ec8 = 1:  ed49d4bc090707090608de929fbf7d8497f35ec8 hello10
2:  73afbb7e27dac8cade2dd014271d53baab714f1f ! 2:  8fc97446be021b44e534421265a138c5bb39a3dc hello11
    @@ Metadata
     Author: ZheNing Hu <adlternative@gamil.com>
    
      ## Commit message ##
    -    hello11
    +    hello12
    
      ## 11 (new) ##
     @@
3:  a625ed274af3614a724bafc4c8ac6fec6b301e38 < -:  ---------------------------------------- hellodev4
-:  ---------------------------------------- > 3:  88fb2c406f21d745cda4aa2911ab0c5a147f4b70 hellodev3
`

	commitPairs := getRangeDiffs(t, rawRangeDiff)
	expectedCommitPairs := []*CommitPair{
		{
			FromCommit:         "ed49d4bc090707090608de929fbf7d8497f35ec8",
			ToCommit:           "ed49d4bc090707090608de929fbf7d8497f35ec8",
			Comparison:         gitalypb.RangeDiffResponse_COMPARATOR_EQUAL_UNSPECIFIED,
			CommitMessageTitle: "hello10",
		},
		{
			FromCommit:         "73afbb7e27dac8cade2dd014271d53baab714f1f",
			ToCommit:           "8fc97446be021b44e534421265a138c5bb39a3dc",
			Comparison:         gitalypb.RangeDiffResponse_COMPARATOR_NOT_EQUAL,
			CommitMessageTitle: "hello11",
			PatchData: []byte(`@@ Metadata
 Author: ZheNing Hu <adlternative@gamil.com>

  ## Commit message ##
-    hello11
+    hello12

  ## 11 (new) ##
 @@
`),
		},
		{
			FromCommit:         "a625ed274af3614a724bafc4c8ac6fec6b301e38",
			ToCommit:           "----------------------------------------",
			Comparison:         gitalypb.RangeDiffResponse_COMPARATOR_LESS_THAN,
			CommitMessageTitle: "hellodev4",
		},
		{
			FromCommit:         "----------------------------------------",
			ToCommit:           "88fb2c406f21d745cda4aa2911ab0c5a147f4b70",
			Comparison:         gitalypb.RangeDiffResponse_COMPARATOR_GREATER_THAN,
			CommitMessageTitle: "hellodev3",
		},
	}

	require.Equal(t, expectedCommitPairs, commitPairs)
}

func TestRangeDiffParserWithMultipleNotEqual(t *testing.T) {
	t.Parallel()
	rawRangeDiff := `1:  ed49d4bc090707090608de929fbf7d8497f35ec8 < -:  ---------------------------------------- hello10
2:  73afbb7e27dac8cade2dd014271d53baab714f1f < -:  ---------------------------------------- hello11
3:  792613349fd207d7e5d943bdb0a401c9019fcd54 < -:  ---------------------------------------- hellorrr
4:  98f852846374aa371fbdb06aa7dba5f1cbb9f0d9 < -:  ---------------------------------------- helloggg
5:  39bcbf19bab17c1955d92ae11e2267cc92a6c533 ! 1:  553bb380d8c0febaa9c6767f0edc28af074279e0 hellocr-1
    @@ Metadata
      ## Commit message ##
         hellocr-1
    
    -    add message
    -
      ## cr-1 (new) ##
     @@
     +cr-1
6:  464c0756de77fe5f7cced8a7ee90b93d83225c15 ! 2:  f4ddf890100a31918629fb813ca4b8147897cb42 helloxx
    @@
      ## Metadata ##
    -Author: asd <asd>
    +Author: ZheNing Hu <adlternative@gamil.com>
    
      ## Commit message ##
         helloxx
`

	commitPairs := getRangeDiffs(t, rawRangeDiff)
	expectedCommitPairs := []*CommitPair{
		{
			FromCommit:         "ed49d4bc090707090608de929fbf7d8497f35ec8",
			ToCommit:           "----------------------------------------",
			Comparison:         gitalypb.RangeDiffResponse_COMPARATOR_LESS_THAN,
			CommitMessageTitle: "hello10",
		},
		{
			FromCommit:         "73afbb7e27dac8cade2dd014271d53baab714f1f",
			ToCommit:           "----------------------------------------",
			Comparison:         gitalypb.RangeDiffResponse_COMPARATOR_LESS_THAN,
			CommitMessageTitle: "hello11",
		},
		{
			FromCommit:         "792613349fd207d7e5d943bdb0a401c9019fcd54",
			ToCommit:           "----------------------------------------",
			Comparison:         gitalypb.RangeDiffResponse_COMPARATOR_LESS_THAN,
			CommitMessageTitle: "hellorrr",
		},
		{
			FromCommit:         "98f852846374aa371fbdb06aa7dba5f1cbb9f0d9",
			ToCommit:           "----------------------------------------",
			Comparison:         gitalypb.RangeDiffResponse_COMPARATOR_LESS_THAN,
			CommitMessageTitle: "helloggg",
		},
		{
			FromCommit:         "39bcbf19bab17c1955d92ae11e2267cc92a6c533",
			ToCommit:           "553bb380d8c0febaa9c6767f0edc28af074279e0",
			Comparison:         gitalypb.RangeDiffResponse_COMPARATOR_NOT_EQUAL,
			CommitMessageTitle: "hellocr-1",
			PatchData: []byte(`@@ Metadata
  ## Commit message ##
     hellocr-1

-    add message
-
  ## cr-1 (new) ##
 @@
 +cr-1
`),
		},
		{
			FromCommit:         "464c0756de77fe5f7cced8a7ee90b93d83225c15",
			ToCommit:           "f4ddf890100a31918629fb813ca4b8147897cb42",
			Comparison:         gitalypb.RangeDiffResponse_COMPARATOR_NOT_EQUAL,
			CommitMessageTitle: "helloxx",
			PatchData: []byte(`@@
  ## Metadata ##
-Author: asd <asd>
+Author: ZheNing Hu <adlternative@gamil.com>

  ## Commit message ##
     helloxx
`),
		},
	}

	require.Equal(t, expectedCommitPairs, commitPairs)
}

func TestRangeDiffParserWithNotEqualWithNoPatchData(t *testing.T) {
	t.Parallel()
	rawRangeDiff := `1:  ed49d4bc090707090608de929fbf7d8497f35ec8 = 1:  ed49d4bc090707090608de929fbf7d8497f35ec8 hello10
2:  73afbb7e27dac8cade2dd014271d53baab714f1f ! 2:  8fc97446be021b44e534421265a138c5bb39a3dc hello11
3:  a625ed274af3614a724bafc4c8ac6fec6b301e38 < -:  ---------------------------------------- hellodev4
-:  ---------------------------------------- > 3:  88fb2c406f21d745cda4aa2911ab0c5a147f4b70 hellodev3
`

	commitPairs := getRangeDiffs(t, rawRangeDiff)
	expectedCommitPairs := []*CommitPair{
		{
			FromCommit:         "ed49d4bc090707090608de929fbf7d8497f35ec8",
			ToCommit:           "ed49d4bc090707090608de929fbf7d8497f35ec8",
			Comparison:         gitalypb.RangeDiffResponse_COMPARATOR_EQUAL_UNSPECIFIED,
			CommitMessageTitle: "hello10",
		},
		{
			FromCommit:         "73afbb7e27dac8cade2dd014271d53baab714f1f",
			ToCommit:           "8fc97446be021b44e534421265a138c5bb39a3dc",
			Comparison:         gitalypb.RangeDiffResponse_COMPARATOR_NOT_EQUAL,
			CommitMessageTitle: "hello11",
		},
		{
			FromCommit:         "a625ed274af3614a724bafc4c8ac6fec6b301e38",
			ToCommit:           "----------------------------------------",
			Comparison:         gitalypb.RangeDiffResponse_COMPARATOR_LESS_THAN,
			CommitMessageTitle: "hellodev4",
		},
		{
			FromCommit:         "----------------------------------------",
			ToCommit:           "88fb2c406f21d745cda4aa2911ab0c5a147f4b70",
			Comparison:         gitalypb.RangeDiffResponse_COMPARATOR_GREATER_THAN,
			CommitMessageTitle: "hellodev3",
		},
	}

	require.Equal(t, expectedCommitPairs, commitPairs)
}

func TestRangeDiffParserWithNotEqualWithSHA256ObjectHash(t *testing.T) {
	t.Parallel()
	rawRangeDiff := `1:  bbd9fd380826c6cef78871f62b3fb8cf4a466fa99a32e61ea9ba839dc1833e5d = 1:  bbd9fd380826c6cef78871f62b3fb8cf4a466fa99a32e61ea9ba839dc1833e5d hello10
2:  edc718e09a72ae0ba2cc99d54a406d6034f71b572a19f85c408a22c5d63f117b ! 2:  dc460da4ad72c482231e28e688e01f2778a88ce31a08826899d54ef7183998b5 hello11
    @@ Metadata
     Author: ZheNing Hu <adlternative@gamil.com>
    
      ## Commit message ##
    -    hello11
    +    hello12
    
      ## 11 (new) ##
     @@
3:  ec9b2b48e8ebb820773bc80ab1903f41c76c25c4111307279207def635b7cd73 < -:  ---------------------------------------------------------------- hellodev4
-:  ---------------------------------------------------------------- > 3:  8f8eea956d0ea50d6442fdab213326f75bb6f584268b0795ad452faa85db5f9d hellodev3
`

	commitPairs := getRangeDiffs(t, rawRangeDiff)
	expectedCommitPairs := []*CommitPair{
		{
			FromCommit:         "bbd9fd380826c6cef78871f62b3fb8cf4a466fa99a32e61ea9ba839dc1833e5d",
			ToCommit:           "bbd9fd380826c6cef78871f62b3fb8cf4a466fa99a32e61ea9ba839dc1833e5d",
			Comparison:         gitalypb.RangeDiffResponse_COMPARATOR_EQUAL_UNSPECIFIED,
			CommitMessageTitle: "hello10",
		},
		{
			FromCommit:         "edc718e09a72ae0ba2cc99d54a406d6034f71b572a19f85c408a22c5d63f117b",
			ToCommit:           "dc460da4ad72c482231e28e688e01f2778a88ce31a08826899d54ef7183998b5",
			Comparison:         gitalypb.RangeDiffResponse_COMPARATOR_NOT_EQUAL,
			CommitMessageTitle: "hello11",
			PatchData: []byte(`@@ Metadata
 Author: ZheNing Hu <adlternative@gamil.com>

  ## Commit message ##
-    hello11
+    hello12

  ## 11 (new) ##
 @@
`),
		},
		{
			FromCommit:         "ec9b2b48e8ebb820773bc80ab1903f41c76c25c4111307279207def635b7cd73",
			ToCommit:           "----------------------------------------------------------------",
			Comparison:         gitalypb.RangeDiffResponse_COMPARATOR_LESS_THAN,
			CommitMessageTitle: "hellodev4",
		},
		{
			FromCommit:         "----------------------------------------------------------------",
			ToCommit:           "8f8eea956d0ea50d6442fdab213326f75bb6f584268b0795ad452faa85db5f9d",
			Comparison:         gitalypb.RangeDiffResponse_COMPARATOR_GREATER_THAN,
			CommitMessageTitle: "hellodev3",
		},
	}

	require.Equal(t, expectedCommitPairs, commitPairs)
}

func TestRangeDiffParserWithBigPatchBlackBox(t *testing.T) {
	t.Parallel()

	var rawRangeDiff string
	firstNumber := 1
	secondNumber := 1
	repeatTime := 100
	mockPatchData := strings.Repeat("mock data\n", 3)
	commitMessageTitle := "mock commit message title"

	for i := 0; i < repeatTime; i++ {
		// random comparison
		comparator := gitalypb.RangeDiffResponse_Comparator(rand.Intn(int(gitalypb.RangeDiffResponse_COMPARATOR_NOT_EQUAL) + 1))
		comparatorSymbol := ParseCompareSymbolToString(comparator)

		var firstNumberString, secondNumberString string
		var firstCommitID, secondCommitID string
		switch comparator {
		case gitalypb.RangeDiffResponse_COMPARATOR_EQUAL_UNSPECIFIED, gitalypb.RangeDiffResponse_COMPARATOR_NOT_EQUAL:
			firstNumberString = fmt.Sprintf("%d", firstNumber)
			secondNumberString = fmt.Sprintf("%d", secondNumber)
			firstCommitID, _ = text.RandomHex(sha1.Size)
			secondCommitID = firstCommitID
			firstNumber++
			secondNumber++
		case gitalypb.RangeDiffResponse_COMPARATOR_LESS_THAN:
			firstNumberString = fmt.Sprintf("%d", firstNumber)
			secondNumberString = "-"
			firstCommitID, _ = text.RandomHex(sha1.Size)
			secondCommitID = "----------------------------------------"
			firstNumber++
		case gitalypb.RangeDiffResponse_COMPARATOR_GREATER_THAN:
			firstNumberString = "-"
			secondNumberString = fmt.Sprintf("%d", secondNumber)
			firstCommitID = "----------------------------------------"
			secondCommitID, _ = text.RandomHex(sha1.Size)
			secondNumber++
		}

		newLine := fmt.Sprintf("%s:  %s %s %s:  %s %s\n", firstNumberString, firstCommitID, comparatorSymbol, secondNumberString, secondCommitID, commitMessageTitle)
		rawRangeDiff += newLine
		if comparator == gitalypb.RangeDiffResponse_COMPARATOR_NOT_EQUAL {
			rawRangeDiff += mockPatchData
		}
	}
	commitPairs := getRangeDiffs(t, rawRangeDiff)
	require.Equal(t, repeatTime, len(commitPairs))
}

func getRangeDiffs(tb testing.TB, rawRangeDiff string) []*CommitPair {
	var commitPairs []*CommitPair
	parser := NewRangeDiffParser(strings.NewReader(rawRangeDiff))
	for parser.Parse() {
		commitPair := parser.CommitPair()
		if commitPair == nil {
			tb.Fatal("commitPair is nil")
		}
		commitPairs = append(commitPairs, commitPair)
	}
	return commitPairs
}
