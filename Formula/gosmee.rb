# typed: false
# frozen_string_literal: true

# This file was generated by GoReleaser. DO NOT EDIT.
class Gosmee < Formula
  desc "gosmee - A webhook and https://smee.io forwarder"
  homepage "https://github.com/chmouel/gosmee"
  version "0.23.2"

  on_macos do
    url "https://github.com/chmouel/gosmee/releases/download/v0.23.2/gosmee_0.23.2_darwin_all.tar.gz"
    sha256 "511855766195aec1e07ca942e94ab444b75407f7ec1d3936afee55143ce113fc"

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
        url "https://github.com/chmouel/gosmee/releases/download/v0.23.2/gosmee_0.23.2_linux_x86_64.tar.gz"
        sha256 "7d89d7ec6d846215a1300d406a05479183ec50b9afc8cbcda6ca622081669293"

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
        url "https://github.com/chmouel/gosmee/releases/download/v0.23.2/gosmee_0.23.2_linux_arm64.tar.gz"
        sha256 "7515454bec5a19e127aab6e31b925d25eb158c1d23f577b7b1e4b951b6af75fd"

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
