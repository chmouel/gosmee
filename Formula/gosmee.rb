# typed: false
# frozen_string_literal: true

# This file was generated by GoReleaser. DO NOT EDIT.
class Gosmee < Formula
  desc "gosmee - A webhook and https://smee.io forwarder"
  homepage "https://github.com/chmouel/gosmee"
  version "0.22.4"

  on_macos do
    url "https://github.com/chmouel/gosmee/releases/download/v0.22.4/gosmee_0.22.4_darwin_all.tar.gz"
    sha256 "4c9eb491f4ec1626797a2a3d20d896a74d35af5e4c89f1b5b7f5cf8e8e48b123"

    def install
      bin.install "gosmee" => "gosmee"
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
      if Hardware::CPU.is_64_bit?
        url "https://github.com/chmouel/gosmee/releases/download/v0.22.4/gosmee_0.22.4_linux_x86_64.tar.gz"
        sha256 "f0251892f054f0e5d2ceb85e8fb5f595fe17ad9c77ae9497e732c1222f61df9f"

        def install
          bin.install "gosmee" => "gosmee"
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
    if Hardware::CPU.arm?
      if Hardware::CPU.is_64_bit?
        url "https://github.com/chmouel/gosmee/releases/download/v0.22.4/gosmee_0.22.4_linux_arm64.tar.gz"
        sha256 "d5cef57a9720ae92857dd3e6250b6f09744ecbdbaeabe7951b80c9190e75a8ae"

        def install
          bin.install "gosmee" => "gosmee"
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
end
