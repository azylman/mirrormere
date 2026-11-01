import { Carousel } from './carousel/Carousel';
import { Header } from './header/Header';
import { VideoStage } from './video/VideoStage';
import { VoiceHud } from './voice/VoiceHud';

export function App() {
  return (
    <div id="mirrormere-app">
      <Header />
      <Carousel />
      <VideoStage />
      <VoiceHud />
    </div>
  );
}
