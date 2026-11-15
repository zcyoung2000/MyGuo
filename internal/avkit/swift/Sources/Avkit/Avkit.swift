import AppKit
import AVKit
import AVFoundation
import CoreImage

@MainActor func makePlayer() -> any Player { PlayerSurface() }

// AVKit owns presentation and system PiP; Go owns playback policy. Ambient
// sampling reduces to 16x16 on the GPU, without copying full frames to Go.
@MainActor final class PlayerSurface: AVPlayerView, Player {
    private var videoOutput: AVPlayerItemVideoOutput?
    private var lastPixelBuffer: CVPixelBuffer?
    private var endedObserver: NSObjectProtocol?
    private var itemObservation: NSKeyValueObservation?
    private var wantsPlay = false
    private var wantsPreroll = false
    private var prerolling = false
    private var prerolled = false
    private var prerollGeneration = 0
    private var seeking = false
    private var pendingSeek: Double?
    private var loadGeneration = 0
    private var didEnd = false
    private var hadFrame = false
    private var playbackRate: Float = 1
    private var lastSample: CFTimeInterval = 0
    private var lastSamplePosition = -1.0
    private var ambientLeft = [20,22,32]
    private var ambientTop = [20,22,32]
    private let imageContext = CIContext(options: [.cacheIntermediates: false])
    private var pipActive = false

    init() {
        super.init(frame: .zero)
        wantsLayer = true
        layer?.backgroundColor = NSColor.black.cgColor
        player = AVPlayer()
        player?.automaticallyWaitsToMinimizeStalling = false
        player?.preventsDisplaySleepDuringVideoPlayback = true
        controlsStyle = .none
        allowsPictureInPicturePlayback = true
        showsFullScreenToggleButton = false
        updatesNowPlayingInfoCenter = false
        videoGravity = .resizeAspect
        pictureInPictureDelegate = self
    }
    required init?(coder: NSCoder) { fatalError("init(coder:) is not supported") }

    func load(url: String) throws {
        guard let parsed = URL(string: url), ["http","https","file"].contains(parsed.scheme ?? "") else {
            throw NSError(domain: "MyguoPlayer", code: 1, userInfo: [NSLocalizedDescriptionKey:"播放地址无效"])
        }
        pause()
        loadGeneration += 1
        pendingSeek = nil; seeking = false
        itemObservation?.invalidate(); itemObservation = nil
        if let observer = endedObserver { NotificationCenter.default.removeObserver(observer) }
        if let output = videoOutput, let item = player?.currentItem { item.remove(output) }
        didEnd = false
        hadFrame = false
        prerolled = false
        lastPixelBuffer = nil
        lastSamplePosition = -1
        let item = AVPlayerItem(url: parsed)
        item.preferredForwardBufferDuration = 12 * Double(playbackRate)
        let output = AVPlayerItemVideoOutput(pixelBufferAttributes: [
            kCVPixelBufferPixelFormatTypeKey as String:kCVPixelFormatType_420YpCbCr8BiPlanarVideoRange,
            kCVPixelBufferMetalCompatibilityKey as String:true,
        ])
        item.add(output)
        videoOutput = output
        player?.replaceCurrentItem(with:item)
        itemObservation = item.observe(\.status, options:[.initial,.new]) { [weak self,weak item] _,_ in
            Task { @MainActor [weak self,weak item] in
                guard let self,let item,self.player?.currentItem === item else { return }
                self.beginWhenReady()
            }
        }
        endedObserver = NotificationCenter.default.addObserver(forName:.AVPlayerItemDidPlayToEndTime, object:item, queue:.main) { [weak self] _ in self?.didEnd = true }
    }
    func play() {
        if ProcessInfo.processInfo.environment["MYGUO_AVKIT_TRACE"] == "1" {FileHandle.standardError.write(Data("[avkit] play ready=\(String(describing:player?.currentItem?.status))\n".utf8))}
        didEnd = false; wantsPlay = true; beginWhenReady()
    }
    func pause() {
        if ProcessInfo.processInfo.environment["MYGUO_AVKIT_TRACE"] == "1" {FileHandle.standardError.write(Data("[avkit] pause\n".utf8))}
        wantsPlay = false; wantsPreroll = false
        cancelPreroll()
        player?.currentItem?.canUseNetworkResourcesForLiveStreamingWhilePaused = false
        player?.pause()
    }
    private func cancelPreroll() {
        prerollGeneration += 1
        if prerolling { player?.cancelPendingPrerolls() }
        prerolling = false
    }
    private func beginWhenReady() {
        guard player?.currentItem?.status == .readyToPlay else { return }
        if pendingSeek != nil { performPendingSeek() }
        else if wantsPlay && !seeking {
            wantsPreroll = false; cancelPreroll()
            player?.currentItem?.canUseNetworkResourcesForLiveStreamingWhilePaused = false
            player?.playImmediately(atRate:playbackRate)
        } else if wantsPreroll && !seeking && !prerolling, let player, player.rate == 0 {
            prerolling = true
            let generation = prerollGeneration
            let item = player.currentItem
            player.preroll(atRate:playbackRate) { [weak self,weak item] finished in
                Task { @MainActor [weak self,weak item] in
                    guard let self,let item,generation == self.prerollGeneration,self.player?.currentItem === item else {return}
                    self.prerolling = false
                    if finished { self.wantsPreroll = false; self.prerolled = true }
                }
            }
        }
    }
    func seek(seconds: Double) {
        guard seconds.isFinite else { return }
        didEnd = false
        pendingSeek = max(0,seconds)
        if player?.currentItem?.status == .readyToPlay { performPendingSeek() }
    }
    private func performPendingSeek() {
        guard !seeking,let target = pendingSeek,let player,let item = player.currentItem,item.status == .readyToPlay else { return }
        pendingSeek = nil; seeking = true
        prerolled = false
        cancelPreroll()
        player.pause()
        let generation = loadGeneration
        player.seek(to:CMTime(seconds:target,preferredTimescale:600),toleranceBefore:.zero,toleranceAfter:.zero) { [weak self,weak item] _ in
            Task { @MainActor [weak self,weak item] in
                guard let self,let item,generation == self.loadGeneration,self.player?.currentItem === item else { return }
                self.seeking = false
                self.beginWhenReady()
            }
        }
    }
    func configure(rate: Double, volume: Double, muted: Bool, fit: String) {
        let active = (player?.rate ?? 0) != 0
        let nextRate = Float(min(2,max(0.25,rate)))
        if nextRate != playbackRate { prerolled = false; cancelPreroll() }
        playbackRate = nextRate
        player?.currentItem?.preferredForwardBufferDuration = 12 * Double(playbackRate)
        player?.volume = Float(min(1,max(0,volume)))
        player?.isMuted = muted
        player?.currentItem?.audioTimePitchAlgorithm = .timeDomain
        videoGravity = fit == "cover" ? .resizeAspectFill : .resizeAspect
        if active { player?.rate = playbackRate }
    }
    func preroll() {
        guard !wantsPlay else { return }
        player?.currentItem?.canUseNetworkResourcesForLiveStreamingWhilePaused = true
        guard !prerolled else {return}
        wantsPreroll = true
        beginWhenReady()
    }
    func snapshot() -> Status {
        let item = player?.currentItem
        let position = player?.currentTime().seconds ?? 0
        let duration = item?.duration.seconds ?? 0
        var buffered = 0.0
        for value in item?.loadedTimeRanges ?? [] {
            let range = value.timeRangeValue
            if range.start.seconds <= position+0.05 && CMTimeRangeGetEnd(range).seconds >= position {
                buffered = max(buffered,CMTimeRangeGetEnd(range).seconds)
            }
        }
        let pictureReady = isReadyForDisplay || (videoOutput?.hasNewPixelBuffer(forItemTime:player?.currentTime() ?? .zero) ?? false)
        hadFrame = hadFrame || pictureReady
        if pictureReady && CACurrentMediaTime()-lastSample >= 0.12 && (lastSamplePosition < 0 || abs(position-lastSamplePosition)>0.01) { sampleAmbient(); lastSamplePosition = position }
        let size = item?.presentationSize ?? .zero
        let seekable = (item?.seekableTimeRanges ?? []).map { CMTimeRangeGetEnd($0.timeRangeValue).seconds }.filter { $0.isFinite }.max() ?? 0
        return Status(position:position.isFinite ? max(0,position):0,
                      duration:duration.isFinite ? max(0,duration):0,
                      buffered:buffered.isFinite ? max(0,buffered):0,
                      seekable:seekable,
                      ready:hadFrame && !seeking && pendingSeek == nil,displayed:isReadyForDisplay,playing:(player?.rate ?? 0)>0,
                      buffering:wantsPlay && (seeking || item?.status != .readyToPlay || player?.timeControlStatus == .waitingToPlayAtSpecifiedRate || (item?.isPlaybackBufferEmpty ?? false)),
                      ended:didEnd,pictureInPicture:pipActive,width:Int(size.width),height:Int(size.height),
                      error:item?.error?.localizedDescription ?? player?.error?.localizedDescription ?? "",
                      ambientLeft:ambientLeft,ambientTop:ambientTop)
    }
    private func sampleAmbient() {
        lastSample = CACurrentMediaTime()
        guard let output = videoOutput, let pixel = output.copyPixelBuffer(forItemTime:player?.currentTime() ?? .zero,itemTimeForDisplay:nil) else { return }
        lastPixelBuffer = pixel
        let image = CIImage(cvPixelBuffer:pixel)
        guard image.extent.width>0,image.extent.height>0 else { return }
        let small = image.transformed(by:CGAffineTransform(scaleX:16/image.extent.width,y:16/image.extent.height))
        var rgba = [UInt8](repeating:0,count:16*16*4)
        imageContext.render(small,toBitmap:&rgba,rowBytes:64,bounds:CGRect(x:0,y:0,width:16,height:16),format:.RGBA8,colorSpace:CGColorSpaceCreateDeviceRGB())
        func average(_ left: Bool) -> [Int] {
            var rgb = [0,0,0]; var count = 0
            for y in 0..<16 { for x in 0..<16 where left ? x<3:y>=13 {
                let i = (y*16+x)*4
                for c in 0..<3 { rgb[c] += Int(rgba[i+c]) }
                count += 1
            } }
            return rgb.map { $0/max(1,count) }
        }
        ambientLeft = average(true); ambientTop = average(false)
    }
    func captureFrame() -> Data {
        guard let pixel = videoOutput?.copyPixelBuffer(forItemTime:player?.currentTime() ?? .zero,itemTimeForDisplay:nil) ?? lastPixelBuffer else { return Data() }
        let image = CIImage(cvPixelBuffer:pixel)
        let extent = image.extent
        guard extent.width > 0, extent.height > 0 else { return Data() }
        let scale = min(1,960/max(extent.width,extent.height))
        let resized = image.transformed(by:CGAffineTransform(scaleX:scale,y:scale))
        guard let cg = imageContext.createCGImage(resized,from:resized.extent) else { return Data() }
        return NSBitmapImageRep(cgImage:cg).representation(using:.jpeg,properties:[.compressionFactor:0.86]) ?? Data()
    }
    func togglePictureInPicture() async throws {
        // Invoke the public NSButton action of AVKit's own PiP control.
        if !pipActive { NSApp.activate(ignoringOtherApps:true); window?.makeKeyAndOrderFront(nil); window?.makeFirstResponder(self) }
        controlsStyle = .inline
        defer { controlsStyle = .none }
        if ProcessInfo.processInfo.environment["MYGUO_AVKIT_TRACE"] == "1" { FileHandle.standardError.write(Data("[avkit-pip] window=\(String(describing:window)) frame=\(frame) hidden=\(isHiddenOrHasHiddenAncestor)\n".utf8)) }
        func pressPictureControl(trace: Bool = false) -> Bool {
            var queue: [AnyObject] = [self]
            if trace {
                for candidate in NSApp.windows {
                    FileHandle.standardError.write(Data("[avkit-pip] app-window=\(candidate.title) type=\(type(of:candidate)) visible=\(candidate.isVisible)\n".utf8))
                    if let root = candidate.contentView { queue.append(root) }
                }
            }
            var visited = Set<ObjectIdentifier>()
            while !queue.isEmpty && visited.count < 1000 {
                let object = queue.removeFirst()
                if !visited.insert(ObjectIdentifier(object)).inserted { continue }
                let element = object as? NSAccessibilityProtocol
                let legacy = object as? NSObject
                func attribute(_ name: NSAccessibility.Attribute) -> Any? {
                    guard let legacy, legacy.accessibilityAttributeNames().contains(name) else { return nil }
                    return legacy.accessibilityAttributeValue(name)
                }
                let role = (attribute(.role) as? String) ?? element?.accessibilityRole()?.rawValue ?? ""
                let labels = [element?.accessibilityLabel() ?? "",element?.accessibilityTitle() ?? "",element?.accessibilityHelp() ?? "",attribute(.title) as? String ?? "",attribute(.description) as? String ?? "",attribute(.help) as? String ?? ""]
                let label = labels.joined(separator:" ").lowercased()
                if trace { FileHandle.standardError.write(Data("[avkit-pip] role=\(role) type=\(type(of:object)) label=\(label)\n".utf8)) }
                if !trace && role == NSAccessibility.Role.button.rawValue && (label.contains("picture in picture") || label.contains("画中画") || label.contains("子母画面") || label.contains("minimize video") || label.contains("restore video") || label.contains("maximize video") || label.contains("maximise video") || label.contains("最大化视频") || label.contains("放大视频") || label.contains("最小化视频") || label.contains("缩小视频") || label.contains("恢复视频")) {
                    if element?.accessibilityPerformPress() == true { return true }
                    if let legacy,legacy.accessibilityActionNames().contains(.press) {
                        legacy.accessibilityPerformAction(.press)
                        return true
                    }
                }
                queue.append(contentsOf:(element?.accessibilityChildren() ?? []).map { $0 as AnyObject })
                if let children = attribute(.children) as? [Any] { queue.append(contentsOf:children.map { $0 as AnyObject }) }
                if let view = object as? NSView { queue.append(contentsOf:view.subviews) }
            }
            return false
        }
        let previousPiP = pipActive
        var actionIssued = false
        for _ in 0..<120 {
            try await Task.sleep(nanoseconds: 30_000_000)
            if pipActive != previousPiP { controlsStyle = .none; return }
            layoutSubtreeIfNeeded()
            if !actionIssued { actionIssued = pressPictureControl() }
        }
        if ProcessInfo.processInfo.environment["MYGUO_AVKIT_TRACE"] == "1" { _ = pressPictureControl(trace:true) }
        controlsStyle = .none
        throw NSError(domain:"MyguoPlayer",code:2,userInfo:[NSLocalizedDescriptionKey:"画中画暂不可用，请等待视频加载完成后重试"])
    }
    func close() throws {
        loadGeneration += 1
        itemObservation?.invalidate(); itemObservation = nil
        pendingSeek = nil;seeking = false
        pause()
        if let observer = endedObserver { NotificationCenter.default.removeObserver(observer) }
        endedObserver = nil
        if let output = videoOutput, let item = player?.currentItem { item.remove(output) }
        videoOutput = nil
        lastPixelBuffer = nil
        player?.replaceCurrentItem(with:nil)
        player = nil
    }
}

extension PlayerSurface: @preconcurrency AVPlayerViewPictureInPictureDelegate {
    func playerViewDidStartPicture(inPicture playerView: AVPlayerView) { pipActive=true;controlsStyle = .none }
    func playerViewDidStopPicture(inPicture playerView: AVPlayerView) { pipActive=false;controlsStyle = .none }
    func playerView(_ playerView: AVPlayerView,failedToStartPictureInPictureWithError error: Error) { FileHandle.standardError.write(Data("[avkit-pip] failed: \(error)\n".utf8)); pipActive=false;controlsStyle = .none }
    func playerView(_ playerView: AVPlayerView,restoreUserInterfaceForPictureInPictureStopWithCompletionHandler completionHandler:(Bool)->Void) {
        window?.makeKeyAndOrderFront(nil);completionHandler(true)
    }
}
