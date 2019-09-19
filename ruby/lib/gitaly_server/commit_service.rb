module GitalyServer
  class CommitService < Gitaly::CommitService::Service
    include Utils
    include Gitlab::EncodingHelper

    def filter_shas_with_signatures(_session, call)
      Enumerator.new do |y|
        repository = nil

        call.each_remote_read.with_index do |request, index|
          repository = Gitlab::Git::Repository.from_gitaly(request.repository, call) if index.zero?

          y << Gitaly::FilterShasWithSignaturesResponse.new(shas: Gitlab::Git::Commit.shas_with_signatures(repository, request.shas))
        end
      end
    end

    def extract_commit_signature(request, call)
      repository = Gitlab::Git::Repository.from_gitaly(request.repository, call)

      Enumerator.new do |y|
        each_commit_signature_chunk(repository, request.commit_id) do |signature_chunk, signed_text_chunk|
          y.yield Gitaly::ExtractCommitSignatureResponse.new(signature: signature_chunk) if signature_chunk.present?

          y.yield Gitaly::ExtractCommitSignatureResponse.new(signed_text: signed_text_chunk) if signed_text_chunk.present?
        end
      end
    end

    private

    # yields either signature chunks or signed_text chunks to the passed block
    def each_commit_signature_chunk(repository, commit_id)
      raise ArgumentError.new("expected a block") unless block_given?

      begin
        signature_text, signed_text = Rugged::Commit.extract_signature(repository.rugged, commit_id)
      rescue Rugged::InvalidError
        raise GRPC::InvalidArgument.new("commit lookup failed for #{commit_id.inspect}")
      rescue Rugged::OdbError
        # The client does not care if the commit does not exist
        return
      end

      signature_text_io = binary_stringio(signature_text)
      loop do
        chunk = signature_text_io.read(Gitlab.config.git.write_buffer_size)
        break if chunk.nil?

        yield chunk, nil
      end

      signed_text_io = binary_stringio(signed_text)
      loop do
        chunk = signed_text_io.read(Gitlab.config.git.write_buffer_size)
        break if chunk.nil?

        yield nil, chunk
      end
    end
  end
end
