# typed: false
# frozen_string_literal: true

# This file was generated by GoReleaser. DO NOT EDIT.
class Gosmee < Formula
  desc "gosmee - A webhook and https://smee.io forwarder"
  homepage "https://github.com/chmouel/gosmee"
  version "0.15.2"

  on_macos do
    url "https://github.com/chmouel/gosmee/releases/download/0.15.2/gosmee_0.15.2_MacOS_all.tar.gz"
    sha256 "4bdb976ae2d713cf5bd3f6a812ae944f5a1f827eefb91b97113fdaca7dee7bf3"

    def install
      output = Utils.popen_read("SHELL=bash #{bin}/gosmee completion bash")
      (bash_completion/"gosmee").write output
      output = Utils.popen_read("SHELL=zsh #{bin}/gosmee completion zsh")
      (zsh_completion/"_gosmee").write output
      output = Utils.popen_read("SHELL=zsh #{bin}/gosmee completion fish")
      (fish_completion/"gosmee.fish").write output
      prefix.install_metafiles
    end
  end

  on_linux do
    if Hardware::CPU.intel?
      url "https://github.com/chmouel/gosmee/releases/download/0.15.2/gosmee_0.15.2_Linux_x86_64.tar.gz"
      sha256 "9c98d8b493ad7ccfe783b24a8c8b2ea29f0bd939a7caa48619189e3ab7b696e7"

      def install
        output = Utils.popen_read("SHELL=bash #{bin}/gosmee completion bash")
        (bash_completion/"gosmee").write output
        output = Utils.popen_read("SHELL=zsh #{bin}/gosmee completion zsh")
        (zsh_completion/"_gosmee").write output
        output = Utils.popen_read("SHELL=zsh #{bin}/gosmee completion fish")
        (fish_completion/"gosmee.fish").write output
        prefix.install_metafiles
      end
    end
    if Hardware::CPU.arm? && Hardware::CPU.is_64_bit?
      url "https://github.com/chmouel/gosmee/releases/download/0.15.2/gosmee_0.15.2_Linux_arm64.tar.gz"
      sha256 "87683ae78fa83bec747c8c9e4409fb28bc68bc72069c42f9d189c69697bbd60f"

      def install
        output = Utils.popen_read("SHELL=bash #{bin}/gosmee completion bash")
        (bash_completion/"gosmee").write output
        output = Utils.popen_read("SHELL=zsh #{bin}/gosmee completion zsh")
        (zsh_completion/"_gosmee").write output
        output = Utils.popen_read("SHELL=zsh #{bin}/gosmee completion fish")
        (fish_completion/"gosmee.fish").write output
        prefix.install_metafiles
      end
    end
  end
end
