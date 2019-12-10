# Gitaly code review process

The Gitaly team uses the following process:

- When you merge request is ready for review, select two approvers from the Merge Request edit view.
- Assign the first reviewer
- When the first reviewer is done, they assign the second reviewer
- When the second reviewer is done
  - If there are no discussions, they are free to merge
  - Otherwise assign back to the author for next round of review.

**Note**: the author-reviewer 1-reviewer 2-author cycle works best with small changes.
With larger changes feel free to use the traditional author-reviewer
1-author-reviewer 1-reviewer 2-author-reviewer 2-cycle.

## Tips for streamlined and thorough reviews

Goals of these tips:

1. Streamline the review and acceptance process: improve throughput
2. Ensure a thorough review: minimize the number of problems that are discovered after merging

### Roles

There is one **contributor**: the person who owns the MR and is trying to get it
merged. There is at least one **reviewer**.

The main review criteria are:

- **readability**: can humans understand the code?
- **correctness**: will a computer do the right thing when running the code?
- **desirability**: do we want this change?

### Tips for the Contributor

As the contributor you have a dual role. You are driving the change in the MR, but you 
are also the **first reviewer** of the MR. Below you will find some tips for reviewing
your own MR. Doing this costs time, but it should also save time for the other
reviewers and thereby speed up the acceptance of your MR.

#### Use the GitLab MR page to put on your "reviewer hat"

It is sometimes hard to switch from being the contributor, to being a reviewer 
of your own work. Looking at your work in the GitLab UI (the merge request page)
helps to get in the reviewer mindset.

#### Reconsider the title of your MR after you reviewed it

When you are in the contributor mindset, you don't always know 
what you are doing, or why. You discover this as you go along. The title you wrote for
your WIP MR when you first pushed it is often not the best title for what is really
going on.

After you have read the diff of your MR in the GitLab UI, take a moment
to think what the title of your MR should be, and update the MR title if needed.

Imagine your title appearing as a [CHANGELOG](CHANGELOG.md) entry.
Will your title give a good indication of what changed?

#### Your MR description should pass the lunch test

Imagine you are having lunch with a colleague and they ask you what you are
working on. You want to tell them about your MR. What do you say to them? This is
roughly what should go in your MR description.

Note that you would probably tell your colleague more than just the
title of the MR. And also more than "I'm fixing issue 1234". The
description should be a summary of _what_ is changing and _why_.

#### Leave notes on your MR as you self-review it

It may feel funny to literally talk to yourself but it works. If
thoughts occur to you as you read your MR, use the comment function
and just write them down.

You probably want to address some or most of your comments before you send your 
MR to another reviewer.

#### Your MR should pass your own review before it goes to a "real" reviewer

- You have done a self-review
- The only unresolved comments from your self-review are questions you don't know
  how to answer
- The MR title clearly describes what is changing
- The MR description explains the "what" and "why", and contains issue links if
  applicable
- The MR is not in an unmergeable WIP ("work in progress") state
- GitalyBot comments have been addressed (labels, changelog etc.)
- The CI build is green

### Tips for the Reviewer

#### Use the "Start/Submit Review" feature of GitLab

The "Start/Submit Review" lets you write comments on a MR that are initially 
only visible to you. Until you submit the review as a whole, you can still add,
change and remove comments.

In order to keep your review focused it is important
to be selective about what you do and do not say, and the Start/Submit feature is very
helpful for this.

#### Ask yourself if your comments are necessary

When you use the "Start/Submit Review" feature you have a unique opportunity to
take things back and leave them unsaid.

Before you submit, look at each of your comments, and ask yourself if it's
necessary to make that comment.

#### Ignore superficial problems if you spot deep problems

You finished a review round and you are about to submit your review with the
"Submit Review" feature. Look at your comments. Do some of them point at a major
problem in the MR?

For example: 
- the MR is solving the wrong problem
- the MR is making a backwards incompatible change
- the MR has a test that does not test the right thing

If your review identifies both major problems and superficial problems, consider
deleting your comments about the superficial problems. The contributor should
spend their energy on the big problems first, and the code with superficial
problems might not be there anymore in the next round.

#### Expect a high standard of readability

Code that is hard to read is bad for several reasons:

- It makes the review slower because the reviewer needs more time to understand
  what is going on.
- It makes it more likely that mistakes / deep problems are hiding in the MR.
- If merged in poorly readable state, it makes all future human interactions
  with this code harder and slower. Interactions can mean: changes, addition,
  bug hunts.

For all these reasons and more it is important to flag things that are hard to
read and ask for them to be improved.

But **be honest**. Ask yourself if a "readability improvement" is really
objectively better, or just a matter of taste.

#### Be thorough in every review round

As a reviewer, it is natural to become less and less thorough in each review
round. Watch out for this. Problems can remain hidden until late in the review.

Sometimes the MR needs several rounds of readability improvements before you
find a deep problem. Ideally, the deep problem is found as early as possible,
but in practice it doesn't always work that way. If you find a deep problem or
a hard question, no matter which review round you're in, you need to bring it
up and at least discuss it.
